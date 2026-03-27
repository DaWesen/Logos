package mq

import (
	"context"
	"fmt"
	"sync"

	"Noah/config"

	"github.com/IBM/sarama"
)

var (
	producer sarama.SyncProducer
	producerOnce sync.Once
)

type Producer struct {
	producer sarama.SyncProducer
}

type Consumer struct {
	consumer sarama.Consumer
}

type MessageHandler func(msg *sarama.ConsumerMessage) error

func InitKafkaProducer() (sarama.SyncProducer, error) {
	var err error
	producerOnce.Do(func() {
		cfg := config.GetConfig()
		kafkaConfig := cfg.Kafka

		config := sarama.NewConfig()
		config.Producer.RequiredAcks = sarama.WaitForAll
		config.Producer.Retry.Max = 5
		config.Producer.Return.Successes = true

		if kafkaConfig.Version != "" {
			version, parseErr := sarama.ParseKafkaVersion(kafkaConfig.Version)
			if parseErr == nil {
				config.Version = version
			}
		}

		producer, err = sarama.NewSyncProducer(kafkaConfig.Brokers, config)
		if err != nil {
			err = fmt.Errorf("failed to create kafka producer: %w", err)
			return
		}
	})

	return producer, err
}

func NewProducer(producer sarama.SyncProducer) *Producer {
	return &Producer{
		producer: producer,
	}
}

func (p *Producer) SendMessage(ctx context.Context, topic string, key string, value []byte) (partition int32, offset int64, err error) {
	msg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(value),
	}

	return p.producer.SendMessage(msg)
}

func (p *Producer) SendUserEvent(ctx context.Context, key string, value []byte) error {
	cfg := config.GetConfig()
	topic := cfg.Kafka.Topics["user_events"]
	if topic == "" {
		topic = "user_events"
	}

	_, _, err := p.SendMessage(ctx, topic, key, value)
	return err
}

func (p *Producer) SendKnowledgeEvent(ctx context.Context, key string, value []byte) error {
	cfg := config.GetConfig()
	topic := cfg.Kafka.Topics["knowledge_events"]
	if topic == "" {
		topic = "knowledge_events"
	}

	_, _, err := p.SendMessage(ctx, topic, key, value)
	return err
}

func (p *Producer) SendQuestionEvent(ctx context.Context, key string, value []byte) error {
	cfg := config.GetConfig()
	topic := cfg.Kafka.Topics["question_events"]
	if topic == "" {
		topic = "question_events"
	}

	_, _, err := p.SendMessage(ctx, topic, key, value)
	return err
}

func (p *Producer) Close() error {
	return p.producer.Close()
}

func InitKafkaConsumer() (sarama.Consumer, error) {
	cfg := config.GetConfig()
	kafkaConfig := cfg.Kafka

	config := sarama.NewConfig()
	config.Consumer.Return.Errors = true

	if kafkaConfig.Version != "" {
		version, parseErr := sarama.ParseKafkaVersion(kafkaConfig.Version)
		if parseErr == nil {
			config.Version = version
		}
	}

	consumer, err := sarama.NewConsumer(kafkaConfig.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka consumer: %w", err)
	}

	return consumer, nil
}

func NewConsumer(consumer sarama.Consumer) *Consumer {
	return &Consumer{
		consumer: consumer,
	}
}

func (c *Consumer) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	partitionList, err := c.consumer.Partitions(topic)
	if err != nil {
		return err
	}

	for _, partition := range partitionList {
		pc, err := c.consumer.ConsumePartition(topic, partition, sarama.OffsetNewest)
		if err != nil {
			return err
		}

		go func(pc sarama.PartitionConsumer) {
			defer pc.Close()
			for {
				select {
				case msg := <-pc.Messages():
					if err := handler(msg); err != nil {
						continue
					}
				case <-pc.Errors():
					continue
				case <-ctx.Done():
					return
				}
			}
		}(pc)
	}

	return nil
}

func (c *Consumer) Close() error {
	return c.consumer.Close()
}
