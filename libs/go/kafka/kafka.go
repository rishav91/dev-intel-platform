// Package kafka wraps segmentio/kafka-go with thin helpers for the spine.
// Works against Redpanda (Kafka API) in the local stack.
package kafka

import (
	"context"
	"time"

	kgo "github.com/segmentio/kafka-go"
)

// Writer wraps a kafka-go Writer bound to a single topic.
type Writer struct{ w *kgo.Writer }

// NewWriter creates a Writer for the given brokers + topic.
func NewWriter(brokers []string, topic string) *Writer {
	return &Writer{w: &kgo.Writer{
		Addr:                   kgo.TCP(brokers...),
		Topic:                  topic,
		Balancer:               &kgo.Hash{}, // key-based partitioning (per-tenant/per-delivery ordering)
		AllowAutoTopicCreation: true,        // convenience for the dev stack
		RequiredAcks:           kgo.RequireAll,
	}}
}

// Write publishes one message with the given key, value, and headers.
func (w *Writer) Write(ctx context.Context, key string, value []byte, headers map[string]string) error {
	hs := make([]kgo.Header, 0, len(headers))
	for k, v := range headers {
		hs = append(hs, kgo.Header{Key: k, Value: []byte(v)})
	}
	return w.w.WriteMessages(ctx, kgo.Message{Key: []byte(key), Value: value, Headers: hs})
}

func (w *Writer) Close() error { return w.w.Close() }

// TopicWriter publishes to a topic chosen per message (the topic isn't bound at
// construction). Used by the outbox relay, which fans rows out to whatever topic
// each row names.
type TopicWriter struct{ w *kgo.Writer }

// NewTopicWriter creates a writer with no fixed topic; each Write names its own.
func NewTopicWriter(brokers []string) *TopicWriter {
	return &TopicWriter{w: &kgo.Writer{
		Addr:                   kgo.TCP(brokers...),
		Balancer:               &kgo.Hash{}, // key-based partitioning (per-tenant ordering)
		AllowAutoTopicCreation: true,
		RequiredAcks:           kgo.RequireAll,
	}}
}

// Write publishes one message to the named topic with the given key/value/headers.
func (w *TopicWriter) Write(ctx context.Context, topic, key string, value []byte, headers map[string]string) error {
	hs := make([]kgo.Header, 0, len(headers))
	for k, v := range headers {
		hs = append(hs, kgo.Header{Key: k, Value: []byte(v)})
	}
	return w.w.WriteMessages(ctx, kgo.Message{Topic: topic, Key: []byte(key), Value: value, Headers: hs})
}

func (w *TopicWriter) Close() error { return w.w.Close() }

// Reader wraps a kafka-go Reader (consumer group) for a single topic.
type Reader struct{ r *kgo.Reader }

// NewReader creates a consumer-group Reader.
func NewReader(brokers []string, groupID, topic string) *Reader {
	return &Reader{r: kgo.NewReader(kgo.ReaderConfig{
		Brokers:        brokers,
		GroupID:        groupID,
		Topic:          topic,
		MinBytes:       1,
		MaxBytes:       10e6,
		CommitInterval: 0, // commit explicitly after successful processing
		MaxWait:        500 * time.Millisecond,
	})}
}

// Message is a decoded inbound message.
type Message struct {
	Key     string
	Value   []byte
	Headers map[string]string
	raw     kgo.Message
}

// Fetch reads the next message without committing its offset.
func (r *Reader) Fetch(ctx context.Context) (Message, error) {
	m, err := r.r.FetchMessage(ctx)
	if err != nil {
		return Message{}, err
	}
	hs := make(map[string]string, len(m.Headers))
	for _, h := range m.Headers {
		hs[h.Key] = string(h.Value)
	}
	return Message{Key: string(m.Key), Value: m.Value, Headers: hs, raw: m}, nil
}

// Commit marks a message processed (at-least-once: process, then commit).
func (r *Reader) Commit(ctx context.Context, m Message) error {
	return r.r.CommitMessages(ctx, m.raw)
}

func (r *Reader) Close() error { return r.r.Close() }
