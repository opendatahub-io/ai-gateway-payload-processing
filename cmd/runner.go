/*
Copyright 2026 The opendatahub.io Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"time"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthPb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/profiling"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/tracing"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/config/loader"
	ippdatalayer "github.com/llm-d/llm-d-inference-payload-processor/pkg/datalayer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/datastore/inmemory"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/modelconfigcollector"
	requestcostmetadata "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/requestcostmetadata"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/datalayer/requestmetadata"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/filter/modelgroup"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker/maxscore"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker/random"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/picker/weightedrandom"
	costguardscorer "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/scorer/costguard"
	inflightrequestsscorer "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/scorer/inflightrequests"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/modelselector/scorer/sessionaffinity"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/basemodelextractor"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/bodyfieldtoheader"
	modelselectorplugin "github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/modelselector"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/requesthandling/profilepicker/single"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/plugins/responsehandling/modelnametoheader"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/handlers"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/metrics"
	runserver "github.com/llm-d/llm-d-inference-payload-processor/pkg/server"
	"github.com/llm-d/llm-d-inference-payload-processor/version"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/dynamicmetadata"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins"
)

var setupLog = ctrl.Log.WithName("setup")

// run is the downstream runner that replaces the upstream runner.NewRunner().Run().
// It reuses all upstream public APIs but wraps the ext-proc server with
// dynamicmetadata.WrapServer before registration, enabling plugins to set
// ProcessingResponse.DynamicMetadata via pseudo-headers.
//
// This file is a hand-maintained copy of the upstream runner, required because
// upstream Runner.Run/AsRunnable offers no hook to wrap the ext-proc server.
// Copied from github.com/llm-d/llm-d-inference-payload-processor
// cmd/runner/runner.go @ v0.1.0-rc.4.0.20260814090434-bf71acdf306a (bf71acd).
// KEEP IN SYNC: when bumping that dependency, re-diff this file against upstream
// — especially handlers.NewServer's signature and registerInTreePlugins, which
// gate whether new in-tree plugins and processing phases are wired in.
func run(ctx context.Context,
	controllers []func(client.Client, *ctrlbuilder.Builder) error,
	customCollectors ...prometheus.Collector,
) error {
	logutil.InitSetupLogging()

	setupLog.Info("ai-gateway-payload-processing build", "commit-sha", version.CommitSHA, "build-ref", version.BuildRef)

	opts := runserver.NewOptions()
	opts.AddFlags(pflag.CommandLine)
	pflag.Parse()

	if err := opts.Complete(); err != nil {
		return err
	}
	if err := opts.Validate(); err != nil {
		setupLog.Error(err, "Failed to validate flags")
		return err
	}

	if opts.Tracing {
		if err := tracing.InitTracing(ctx, setupLog, "llm-d-ipp"); err != nil {
			setupLog.Error(err, "failed to initialize tracing")
			return err
		}
	}

	logutil.InitLogging(&opts.ZapOptions)

	// Dump the full flag set only at DEBUG: values such as --config-text can
	// embed inline plugin configuration, so keep it out of default-level logs
	// (CWE-532). Gated after InitLogging so the operator's verbosity applies.
	// Diverges intentionally from the upstream copy.
	if setupLog.V(logutil.DEBUG).Enabled() {
		flags := make(map[string]any)
		pflag.VisitAll(func(f *pflag.Flag) {
			flags[f.Name] = f.Value
		})
		setupLog.V(logutil.DEBUG).Info("Flags processed", "flags", flags)
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "Failed to get rest config")
		return err
	}

	metrics.Register(customCollectors...)
	metrics.RecordIPPInfo(version.CommitSHA, version.BuildRef)

	metricsServerOptions := metricsserver.Options{
		BindAddress: fmt.Sprintf(":%d", opts.MetricsPort),
		FilterProvider: func() func(c *rest.Config, httpClient *http.Client) (metricsserver.Filter, error) {
			if opts.MetricsEndpointAuth {
				return filters.WithAuthenticationAndAuthorization
			}
			return nil
		}(),
	}

	cacheOptions := cache.Options{}
	namespace := os.Getenv("NAMESPACE")
	if namespace != "" {
		cacheOptions.DefaultNamespaces = map[string]cache.Config{
			namespace: {},
		}
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Cache: cacheOptions, Metrics: metricsServerOptions})
	if err != nil {
		setupLog.Error(err, "Failed to create manager", "config", cfg)
		return err
	}

	if opts.EnablePprof {
		setupLog.Info("Setting pprof handlers")
		if err = profiling.SetupPprofHandlers(mgr); err != nil {
			setupLog.Error(err, "Failed to setup pprof handlers")
			return err
		}
	}

	for _, controllerFunc := range controllers {
		if err := controllerFunc(mgr.GetClient(), ctrl.NewControllerManagedBy(mgr)); err != nil {
			setupLog.Error(err, "Failed to register custom controller")
			return err
		}
	}

	ds := inmemory.NewDatastore()
	processor := ippdatalayer.NewProcessor()
	handle := plugin.NewHandle(ctx, mgr, ds, processor)

	var configBytes []byte
	if opts.ConfigText != "" {
		configBytes = []byte(opts.ConfigText)
	} else if opts.ConfigFile != "" {
		configBytes, err = os.ReadFile(opts.ConfigFile)
		if err != nil {
			setupLog.Error(err, "failed to load config from a file", "file", opts.ConfigFile)
			return fmt.Errorf("failed to load config from a file '%s' - %w", opts.ConfigFile, err)
		}
	}

	registerInTreePlugins()
	plugins.RegisterPlugins()

	theConfig, err := loader.LoadConfiguration(configBytes, handle, processor, setupLog)
	if err != nil {
		return err
	}

	if err := processor.Start(ctx); err != nil {
		setupLog.Error(err, "failed to start datalayer processor")
		return err
	}
	defer processor.Stop()

	// Build the ext-proc handler and wrap it with the dynamic-metadata interceptor.
	server := handlers.NewServer(
		theConfig.PreProcessors,
		theConfig.ProfilePicker,
		theConfig.Profiles,
		theConfig.PostProcessors,
		theConfig.ResponseHeadersPostProcessors,
	).WithEventNotifier(processor)

	wrappedServer := dynamicmetadata.WrapServer(server)

	extProcRunnable, err := newExtProcRunnable(wrappedServer, opts.GRPCPort, opts.SecureServing)
	if err != nil {
		return err
	}
	if err := mgr.Add(noLeaderElection(extProcRunnable)); err != nil {
		setupLog.Error(err, "Failed to register ext-proc gRPC server")
		return err
	}

	healthSrv := grpc.NewServer()
	healthPb.RegisterHealthServer(healthSrv, &ippHealthServer{})
	if err := mgr.Add(noLeaderElection(grpcServerRunnable("health", healthSrv, opts.GRPCHealthPort))); err != nil {
		setupLog.Error(err, "Failed to register health server")
		return err
	}

	setupLog.Info("manager starting")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "Error starting manager")
		return err
	}
	setupLog.Info("manager terminated")
	return nil
}

// registerInTreePlugins registers factory functions for all upstream in-tree plugins.
func registerInTreePlugins() {
	plugin.Register(single.SingleProfilePickerType, single.SingleProfilePickerFactory)
	plugin.Register(bodyfieldtoheader.BodyFieldToHeaderPluginType, bodyfieldtoheader.BodyFieldToHeaderPluginFactory)
	plugin.Register(basemodelextractor.BaseModelToHeaderPluginType, basemodelextractor.BaseModelToHeaderPluginFactory)
	plugin.Register(requestmetadata.PluginType, requestmetadata.ExtractorFactory)
	plugin.Register(requestcostmetadata.PluginType, requestcostmetadata.ExtractorFactory)
	plugin.Register(modelconfigcollector.PluginType, modelconfigcollector.DatasourceFactory)
	// register model selector plugins
	plugin.Register(modelgroup.ModelGroupFilterType, modelgroup.ModelGroupFilterFactory)
	plugin.Register(random.RandomPickerType, random.RandomPickerFactory)
	plugin.Register(maxscore.MaxScorePickerType, maxscore.MaxScorePickerFactory)
	plugin.Register(weightedrandom.WeightedRandomPickerType, weightedrandom.WeightedRandomPickerFactory)
	plugin.Register(modelselectorplugin.ModelSelectorPluginType, modelselectorplugin.ModelSelectorPluginFactory)
	plugin.Register(inflightrequestsscorer.PluginType, inflightrequestsscorer.ScorerFactory)
	plugin.Register(sessionaffinity.PluginType, sessionaffinity.ScorerFactory)
	plugin.Register(costguardscorer.PluginType, costguardscorer.ScorerFactory)
	plugin.Register(modelnametoheader.PluginType, modelnametoheader.PluginFactory)
}

// newExtProcRunnable creates a controller-runtime Runnable that serves the given
// ext-proc server on the specified port, optionally with TLS.
func newExtProcRunnable(extProcServer extProcPb.ExternalProcessorServer, port int, secureServing bool) (manager.Runnable, error) {
	return manager.RunnableFunc(func(ctx context.Context) error {
		var srv *grpc.Server
		if secureServing {
			cert, err := createSelfSignedCert()
			if err != nil {
				return fmt.Errorf("failed to create self signed certificate - %w", err)
			}
			creds := credentials.NewTLS(&tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
				NextProtos:   []string{"h2"},
			})
			srv = grpc.NewServer(grpc.Creds(creds))
		} else {
			srv = grpc.NewServer()
		}

		extProcPb.RegisterExternalProcessorServer(srv, extProcServer)
		return startGRPCServer(ctx, "ext-proc", srv, port)
	}), nil
}

// --- Trivial utilities reimplemented from upstream internal/ packages ---

type leaderElectionRunnable struct {
	manager.Runnable
}

func noLeaderElection(r manager.Runnable) manager.Runnable {
	return &leaderElectionRunnable{Runnable: r}
}

func (r *leaderElectionRunnable) NeedLeaderElection() bool { return false }

func grpcServerRunnable(name string, srv *grpc.Server, port int) manager.Runnable {
	return manager.RunnableFunc(func(ctx context.Context) error {
		return startGRPCServer(ctx, name, srv, port)
	})
}

func startGRPCServer(ctx context.Context, name string, srv *grpc.Server, port int) error {
	logger := ctrl.Log.WithValues("name", name)
	logger.Info("gRPC server starting")

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("gRPC server failed to listen - %w", err)
	}

	logger.Info("gRPC server listening", "port", port)

	doneCh := make(chan struct{})
	defer close(doneCh)
	go func() {
		select {
		case <-ctx.Done():
			logger.Info("gRPC server shutting down")
			srv.GracefulStop()
		case <-doneCh:
		}
	}()

	if err := srv.Serve(lis); err != nil && err != grpc.ErrServerStopped {
		return fmt.Errorf("gRPC server failed - %w", err)
	}
	logger.Info("gRPC server terminated")
	return nil
}

func createSelfSignedCert() (tls.Certificate, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("error creating serial number: %v", err)
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{Organization: []string{"Inference Ext"}},
		NotBefore:             now.UTC(),
		NotAfter:              now.Add(time.Hour * 24 * 365 * 10).UTC(),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("error generating key: %v", err)
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("error creating certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("error marshalling private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})

	return tls.X509KeyPair(certPEM, keyPEM)
}

// ippHealthServer implements the gRPC Health service.
type ippHealthServer struct{}

func (s *ippHealthServer) Check(ctx context.Context, in *healthPb.HealthCheckRequest) (*healthPb.HealthCheckResponse, error) {
	log.FromContext(ctx).V(4).Info("gRPC health check serving", "service", in.Service)
	return &healthPb.HealthCheckResponse{Status: healthPb.HealthCheckResponse_SERVING}, nil
}

func (s *ippHealthServer) List(ctx context.Context, _ *healthPb.HealthListRequest) (*healthPb.HealthListResponse, error) {
	resp, err := s.Check(ctx, &healthPb.HealthCheckRequest{Service: extProcPb.ExternalProcessor_ServiceDesc.ServiceName})
	if err != nil {
		return nil, err
	}
	return &healthPb.HealthListResponse{
		Statuses: map[string]*healthPb.HealthCheckResponse{
			extProcPb.ExternalProcessor_ServiceDesc.ServiceName: resp,
		},
	}, nil
}

func (s *ippHealthServer) Watch(_ *healthPb.HealthCheckRequest, _ healthPb.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}
