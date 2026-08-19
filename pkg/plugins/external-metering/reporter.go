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

package external_metering

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/IBM/sarama"
)

// usageReporter abstracts the transport for sending metering events.
// Implementations must be safe for concurrent use.
type usageReporter interface {
	reportUsage(ctx context.Context, event []byte) error
	Close() error
}

// httpReporter sends events via HTTP POST (legacy transport).
type httpReporter struct {
	client *meteringClient
}

func (r *httpReporter) reportUsage(ctx context.Context, event []byte) error {
	return r.client.reportUsage(ctx, event)
}

func (r *httpReporter) Close() error { return nil }

// kafkaReporter sends events to a Kafka topic with acks=all.
type kafkaReporter struct {
	producer sarama.SyncProducer
	topic    string
}

type kafkaConfig struct {
	Brokers        string
	Topic          string
	TLSCACert      string
	SASLUser       string
	SASLPassFile   string
	TLSEnabled     bool
}

func newKafkaReporter(cfg kafkaConfig) (*kafkaReporter, error) {
	brokers := splitAndTrim(cfg.Brokers, ",")
	if len(brokers) == 0 {
		return nil, fmt.Errorf("kafkaBrokers must not be empty")
	}
	if cfg.Topic == "" {
		return nil, fmt.Errorf("kafkaTopic must not be empty")
	}

	sc := sarama.NewConfig()
	sc.Version = sarama.V3_5_0_0
	sc.Producer.RequiredAcks = sarama.WaitForAll
	sc.Producer.Idempotent = true
	sc.Producer.Return.Successes = true
	sc.Net.MaxOpenRequests = 1

	if cfg.TLSEnabled {
		sc.Net.TLS.Enable = true
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.TLSCACert != "" {
			caCert, err := os.ReadFile(cfg.TLSCACert)
			if err != nil {
				return nil, fmt.Errorf("reading Kafka CA cert %s: %w", cfg.TLSCACert, err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse Kafka CA cert %s", cfg.TLSCACert)
			}
			tlsCfg.RootCAs = pool
		}
		sc.Net.TLS.Config = tlsCfg
	}

	if cfg.SASLUser != "" {
		password, err := os.ReadFile(cfg.SASLPassFile)
		if err != nil {
			return nil, fmt.Errorf("reading SASL password file %s: %w", cfg.SASLPassFile, err)
		}
		trimmed := strings.TrimSpace(string(password))
		if trimmed == "" {
			return nil, fmt.Errorf("SASL password file %s is empty", cfg.SASLPassFile)
		}
		sc.Net.SASL.Enable = true
		sc.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
		sc.Net.SASL.User = cfg.SASLUser
		sc.Net.SASL.Password = trimmed
		sc.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &scramClient{}
		}
	}

	producer, err := sarama.NewSyncProducer(brokers, sc)
	if err != nil {
		return nil, fmt.Errorf("creating Kafka producer: %w", err)
	}

	return &kafkaReporter{
		producer: producer,
		topic:    cfg.Topic,
	}, nil
}

func (r *kafkaReporter) reportUsage(ctx context.Context, event []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish aborted: %w", err)
	}

	msg := &sarama.ProducerMessage{
		Topic: r.topic,
		Value: sarama.ByteEncoder(event),
	}

	_, _, err := r.producer.SendMessage(msg)
	if err != nil {
		return fmt.Errorf("publishing to Kafka topic %s: %w", r.topic, err)
	}
	return nil
}

func (r *kafkaReporter) Close() error {
	return r.producer.Close()
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := parts[:0]
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
