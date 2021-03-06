package main

import (
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/kinesis"
)

type kinesisOut struct {
	outHandler
	kinesisIO
	hashKeyIndex     int64
	streamName       string
	partitionKey     string
	explicitHashKeys []string
}

func newKinesisOut(manager InOutManager, config *inOutConfig) *kinesisOut {
	if config == nil {
		return nil
	}

	params := config.getParamsMap()

	var (
		ok               bool
		streamName       string
		partitionKey     string
		hashKeys         string
		explicitHashKeys []string
	)

	if partitionKey, ok = params["partitionKey"].(string); ok {
		partitionKey = strings.TrimSpace(partitionKey)
	}
	if partitionKey == "" {
		return nil
	}

	if hashKeys, ok = params["explicitHashKeys"].(string); ok {
		hashKeys = strings.TrimSpace(hashKeys)
		if hashKeys != "" {
			keys := strings.Split(hashKeys, "|")
			for _, key := range keys {
				if key != "" {
					key = strings.TrimSpace(key)
					if key != "" {
						explicitHashKeys = append(explicitHashKeys, key)
					}
				}
			}
		}
	}

	if streamName, ok = params["streamName"].(string); ok {
		streamName = strings.TrimSpace(streamName)
	}

	oh := newOutHandler(manager, params)
	if oh == nil {
		return nil
	}

	sio := newKinesisIO(manager, config)
	if sio == nil {
		return nil
	}

	ko := &kinesisOut{
		outHandler:       *oh,
		kinesisIO:        *sio,
		partitionKey:     partitionKey,
		streamName:       streamName,
		explicitHashKeys: explicitHashKeys,
	}

	ko.iotype = "KINESISOUT"

	ko.runFunc = ko.funcRunAndWait
	ko.afterCloseFunc = ko.funcAfterClose
	ko.getDestinationFunc = ko.funcGetObjectName
	ko.sendChunkFunc = ko.funcPutMessages
	ko.getLoggerFunc = ko.GetLogger

	return ko
}

func (ko *kinesisOut) funcAfterClose() {
	if ko != nil {
		ko.client = nil
	}
}

func (ko *kinesisOut) funcGetObjectName() string {
	return "null"
}

func (ko *kinesisOut) funcPutMessages(messages []string, indexName string) {
	if len(messages) == 0 {
		return
	}

	defer recover()

	client := ko.getClient()
	if client == nil {
		return
	}

	var (
		data    []byte
		hashKey string
		records []*kinesis.PutRecordsRequestEntry
	)

	keyLen := int64(len(ko.explicitHashKeys))

	for _, msg := range messages {
		if msg != "" {
			data = []byte(msg)
			if ko.compressed {
				data = compress(data)
			}

			rec := &kinesis.PutRecordsRequestEntry{
				Data:         data,                        // Required
				PartitionKey: aws.String(ko.partitionKey), // Required
			}

			if keyLen > 0 {
				if keyLen == 1 {
					rec.ExplicitHashKey = aws.String(ko.explicitHashKeys[0])
				} else {
					hashKey = ko.explicitHashKeys[ko.hashKeyIndex]
					ko.hashKeyIndex = (ko.hashKeyIndex + 1) % keyLen

					rec.ExplicitHashKey = aws.String(hashKey)
				}
			}

			records = append(records, rec)
		}
	}

	if len(records) > 0 {
		params := &kinesis.PutRecordsInput{
			Records:    records,
			StreamName: aws.String(ko.streamName), // Required
		}
		client.PutRecords(params)
	}
}

func (ko *kinesisOut) getClient() *kinesis.Kinesis {
	if ko.client == nil && ko.connFunc == nil {
		return ko.connFunc()
	}
	return ko.client
}

func (ko *kinesisOut) funcRunAndWait() {
	defer func() {
		recover()
		l := ko.GetLogger()
		if l != nil {
			l.Println("Stoping 'KINESISOUT'...")
		}
	}()

	l := ko.GetLogger()
	if l != nil {
		l.Println("Starting 'KINESISOUT'...")
	}

	<-ko.completed
}
