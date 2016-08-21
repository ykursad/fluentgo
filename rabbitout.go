package main

import (
	"bytes"
	"compress/gzip"

	"github.com/streadway/amqp"
)

type rabbitOut struct {
	rabbitIO
	outHandler
	mandatory bool
	immediate bool
}

func newRabbitOut(manager InOutManager, config *inOutConfig) *rabbitOut {
	if config == nil {
		return nil
	}

	params := make(map[string]interface{}, len(config.Params))
	for _, p := range config.Params {
		params[p.Name] = p.Value
	}

	oh := newOutHandler(manager, params)
	if oh == nil {
		return nil
	}

	rio := newRabbitIO(manager.GetLogger(), params)
	if rio != nil {
		mandatory, _ := params["mandatory"].(bool)
		immediate, _ := params["immediate"].(bool)

		ro := &rabbitOut{
			rabbitIO:   *rio,
			outHandler: *oh,
			mandatory:  mandatory,
			immediate:  immediate,
		}

		ro.runFunc = ro.funcWait
		ro.connFunc = ro.funcSubscribe

		ro.afterCloseFunc = rio.funcAfterClose

		ro.getDestinationFunc = ro.funcChannel
		ro.sendChunkFunc = ro.funcSendMessagesChunk

		return ro
	}
	return nil
}

func (ro *rabbitOut) compress(data []byte) []byte {
	if len(data) > 0 {
		var buff bytes.Buffer
		gzipW := gzip.NewWriter(&buff)

		if gzipW != nil {
			defer gzipW.Close()

			n, err := gzipW.Write(data)
			if err != nil {
				return data
			} else if n > 0 {
				return buff.Bytes()
			}
		}
	}
	return nil
}

func (ro *rabbitOut) funcChannel() string {
	return ""
}

func (ro *rabbitOut) funcSendMessagesChunk(messages []string, channel string) {
	if len(messages) > 0 {
		m := ro.GetManager()
		if m == nil {
			return
		}

		defer func() {
			recover()
			m.DoSleep()
		}()

		var (
			err     error
			channel *amqp.Channel
		)

		var body []byte
		for _, msg := range messages {
			if !(err == nil && ro.Processing() && m.Processing()) {
				break
			}

			if msg != "" {
				err = func() error {
					var sendErr error
					defer func() {
						sendErr, _ = recover().(error)
					}()

					ro.Connect()

					channel = ro.channel
					if channel != nil {
						body = []byte(msg)
						if ro.compressed {
							body = ro.compress(body)
						}
						if len(body) > 0 {
							sendErr = channel.Publish(
								ro.exchange,  // exchange
								ro.queue,     // routing key
								ro.mandatory, // mandatory
								ro.immediate, // immediate
								amqp.Publishing{
									ContentType: ro.contentType,
									Body:        body,
								})
						}
					}
					return sendErr
				}()
			}
		}
	}
}

func (ro *rabbitOut) funcWait() {
	defer func() {
		recover()
		l := ro.GetLogger()
		if l != nil {
			l.Println("Stoping 'REDISOUT'...")
		}
	}()

	l := ro.GetLogger()
	if l != nil {
		l.Println("Starting 'REDISOUT'...")
	}

	<-ro.completed
}