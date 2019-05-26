package beater

import (
	"encoding/json"
	"fmt"
	"time"
	
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/guardduty"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/logp"
	"github.com/mitchellh/mapstructure"
	
	"github.com/forter/guarddutybeat/config"
)

// Guarddutybeat configuration.
type Guarddutybeat struct {
	done   chan struct{}
	config config.Config
	client beat.Client
	sqs        *sqs.SQS
	queueURL   string
	logger      *logp.Logger
}

// New creates an instance of guarddutybeat.
func New(b *beat.Beat, cfg *common.Config) (beat.Beater, error) {
	c := config.DefaultConfig
	if err := cfg.Unpack(&c); err != nil {
		return nil, fmt.Errorf("Error reading config file: %v", err)
	}
	sess := session.Must(session.NewSession())
	svc := sqs.New(sess)
	logger := logp.NewLogger("guarddutybeat-internal")
	queueURLResp, err := svc.GetQueueUrl(&sqs.GetQueueUrlInput{
		QueueName:              aws.String(c.SQSQueueName),
		QueueOwnerAWSAccountId: aws.String(c.AccountID),
	})
	if err != nil {
		logger.Error("Could not get Queue Name")
		return nil, err
	}

	bt := &Guarddutybeat{
		done:   make(chan struct{}),
		config: c,
		sqs: svc,
		logger: logger,
		queueURL: *queueURLResp.QueueUrl,
	}
	return bt, nil
}

func pullEvents(bt *Guarddutybeat) ([]guardduty.Finding, error) {
	result, err := bt.sqs.ReceiveMessage(&sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(bt.queueURL),
		MaxNumberOfMessages: aws.Int64(10),
		WaitTimeSeconds:     aws.Int64(0),
	})
	if err != nil {
		return nil, err
	}
	bt.logger.Info("Number of SQS messages found", len(result.Messages))
	if len(result.Messages) == 0 {
		bt.logger.Info("No SQS Messages Found")
	}
	var toReturn []guardduty.Finding
	for _, message := range result.Messages {
		body := message.Body
		handle := message.ReceiptHandle
		_, err := bt.sqs.DeleteMessage(&sqs.DeleteMessageInput{
			QueueUrl:      aws.String(bt.queueURL),
			ReceiptHandle: handle,
		})
		
		if err != nil {
			bt.logger.Error("Error acking message", err)
			return nil, err
		}
		var event events.SNSEntity
		err = json.Unmarshal([]byte(*body), &event)
		if err != nil {
			bt.logger.Error("Error decoding json", err)
			return nil, err
		}
		var finding guardduty.Finding
		err = json.Unmarshal([]byte(event.Message), &finding)
		toReturn = append(toReturn, finding)
		
	}
	return toReturn, nil
}

// Run starts guarddutybeat.
func (bt *Guarddutybeat) Run(b *beat.Beat) error {
	bt.logger.Info("guarddutybeat is running! Hit CTRL-C to stop it.")

	var err error
	bt.client, err = b.Publisher.Connect()
	if err != nil {
		return err
	}

	ticker := time.NewTicker(bt.config.Period)
	if err != nil {
		bt.logger.Error("Failed to pull events from SQS", err)
	}
	var toSend []beat.Event
	for {
		select {
		case <-bt.done:
			return nil
		case <-ticker.C:
		}
		events, err := pullEvents(bt)
		if err != nil {
			bt.logger.Error(err)
		}
		for _, e := range events {
			var result common.MapStr
			mConfig := &mapstructure.DecoderConfig{
				TagName: "json",
				Result: &result,
			}
			decoder, _ := mapstructure.NewDecoder(mConfig)
			err := decoder.Decode(e)
			if err != nil {
				bt.logger.Error("Could not convert guarddutyevent event into common map")
				return err
			}
			result["type"] = b.Info.Name
			event := beat.Event{
				Timestamp: time.Now(),
				Fields:    result,
			}
			toSend = append(toSend, event)
			bt.client.Publish(event)
			bt.logger.Info("Event sent")
		}
	}
}

// Stop stops guarddutybeat.
func (bt *Guarddutybeat) Stop() {
	bt.client.Close()
	close(bt.done)
}
