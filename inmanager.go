package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type inProvider interface {
	ioClient
}

type bufferFile struct {
	count int
	size  int
	file  *os.File
}

type inManager struct {
	sync.Mutex
	processing      int32
	poppingQueue    int32
	indexer         int64
	inputDir        string
	outputDir       string
	prefix          string
	extension       string
	maxCount        int
	maxSize         int
	maxMessageSize  int
	flushOnEverySec time.Duration
	lastFlushTime   time.Time
	lastQueueTime   time.Time
	timestampKey    string
	timestampFormat string
	inputs          []inProvider
	logger          Logger
	inQ             *inQueue
	bufFile         *bufferFile
	completed       chan bool
}

func newInManager(config *fluentConfig, logger Logger) *inManager {
	if logger == nil {
		logger = newDummyLogger()
	}

	inputDir := (&config.Inputs.Buffer).getPath()
	if exists, err := pathExists(inputDir); !exists || err != nil {
		os.MkdirAll(inputDir, 0777)
	}

	outputDir := inputDir + "completed" + string(os.PathSeparator)
	if exists, err := pathExists(outputDir); !exists || err != nil {
		os.MkdirAll(outputDir, 0777)
	}

	manager := &inManager{
		indexer:         int64(-1),
		inputDir:        inputDir,
		outputDir:       outputDir,
		lastFlushTime:   time.Now(),
		lastQueueTime:   time.Now(),
		logger:          logger,
		flushOnEverySec: (&config.Inputs.Buffer.Flush).getOnEverySec(),
		timestampKey:    strings.TrimSpace(config.Inputs.Buffer.TimestampKey),
		timestampFormat: (&config.Inputs.Buffer).getTimestampFormat(),
		maxMessageSize:  (&config.Inputs.Buffer).getMaxMessageSize(),
		maxCount:        (&config.Inputs.Buffer).getMaxCount(),
		maxSize:         (&config.Inputs.Buffer).getMaxSize(),
		prefix:          (&config.Inputs.Buffer).getPrefix(),
		extension:       (&config.Inputs.Buffer).getExtension(),
		inQ:             newInQueue((&config.Inputs.Queue).getMaxParams()),
	}

	manager.setInputs(&config.Inputs)

	return manager
}

func (m *inManager) GetMaxMessageSize() int {
	return m.maxMessageSize
}

func (m *inManager) GetInQueue() *inQueue {
	return m.inQ
}

func (m *inManager) GetOutQueue() *outQueue {
	return nil
}

func (m *inManager) GetLogger() Logger {
	return m.logger
}

func (m *inManager) Close() {
	defer recover()

	if m.Processing() {
		c := m.completed
		if c != nil {
			defer func() {
				m.completed = nil
			}()
			close(c)
		}
	}
}

func (m *inManager) DoSleep() bool {
	return false
}

func (m *inManager) Processing() bool {
	return atomic.LoadInt32(&m.processing) != 0
}

func (m *inManager) Process() (completed <-chan bool) {
	if atomic.CompareAndSwapInt32(&m.processing, 0, 1) {
		if len(m.inputs) > 0 {
			m.completed = make(chan bool)
			go m.processInputs()
		}
	}
	return m.completed
}

func (m *inManager) setInputs(config *inputsConfig) {
	var result []inProvider

	if config != nil {
		var in inProvider

		for _, p := range config.Producers {
			t := strings.ToLower(p.Type)

			in = nil
			if t == "redischan" || t == "redischanin" {
				in = newRedisChanIn(m, &p)
			} else if t == "redislist" || t == "redislistin" {
				in = newRedisListIn(m, &p)
			} else if t == "kinesis" || t == "kinesisin" {
				in = newKinesisIn(m, &p)
			} else if t == "sqs" || t == "sqsin" {
				in = newSqsIn(m, &p)
			} else if t == "rabbit" || t == "rabbitin" {
				in = newRabbitIn(m, &p)
			}

			if in != nil {
				v := reflect.ValueOf(in)
				if v.Kind() != reflect.Ptr || !v.IsNil() {
					result = append(result, in)
				}
			}
		}
	}

	if result != nil {
		m.inputs = result
	} else {
		m.inputs = make([]inProvider, 0)
	}
}

func (m *inManager) appendTimestamp(data []byte) []byte {
	if len(data) > 0 {
		timestampKey := m.timestampKey
		if timestampKey != "" {
			defer recover()

			var j interface{}
			err := json.Unmarshal(data, &j)

			if err == nil && j != nil {
				msg, ok := j.(map[string]interface{})
				if ok && msg != nil {
					_, ok := msg[timestampKey]
					if !ok {
						msg[timestampKey] = time.Now().Format(m.timestampFormat)

						stamped, err := json.Marshal(msg)
						if err == nil {
							return stamped
						}
					}
				}
			}
		}
	}
	return data
}

func (m *inManager) processQueue() {
	if !atomic.CompareAndSwapInt32(&m.poppingQueue, m.poppingQueue, int32(1)) {
		return
	}

	defer func() {
		defer atomic.StoreInt32(&m.poppingQueue, int32(0))
		recover()
	}()
	m.lastQueueTime = time.Now()

	var (
		data []byte
		ok   bool
	)

	var ln int

	for m.Processing() && m.inQ.Count() > 0 {
		data, ok = m.inQ.Pop()
		if !ok {
			break
		}

		ln = len(data)
		if ln > 0 && (m.maxMessageSize < 1 || ln <= m.maxMessageSize) {
			data = m.appendTimestamp(data)
			m.writeToBuffer(data)
		}
	}
}

func (m *inManager) nextBufferFile() string {
	t := time.Now()
	prefix := m.prefix + fmt.Sprintf("%d%02d%02dT%02dx", t.Year(), t.Month(), t.Day(), t.Hour())

	var (
		exists   bool
		err      error
		rest     string
		bufName1 string
		bufName2 string
	)

	fileName := m.inputDir + prefix
	movedName := m.outputDir + prefix

	start := atomic.AddInt64(&m.indexer, int64(1))
	if start == math.MaxInt64 {
		start = 0
		atomic.StoreInt64(&m.indexer, 0)
	}

	for i := start; i < math.MaxInt64; i++ {
		atomic.StoreInt64(&m.indexer, i)

		rest = fmt.Sprintf("%06d", i) + m.extension

		bufName1, _ = filepath.Abs(fileName + rest)

		exists, err = fileExists(bufName1)
		if !exists && err == nil {
			bufName2, _ = filepath.Abs(movedName + rest)

			exists, err = fileExists(bufName2)
			if !exists && err == nil {
				return bufName1
			}
		}
	}
	return ""
}

func (m *inManager) prepareBuffer(dataLen int) {
	bf := m.bufFile
	dataLen = maxInt(0, dataLen)

	changeFile := bf == nil ||
		(m.maxSize > 0 && bf.size+dataLen > m.maxSize) ||
		(m.maxCount > 0 && bf.count+1 > m.maxCount)

	if !changeFile {
		return
	}

	m.bufFile = nil
	m.completedFile(bf)

	var file *os.File
	filename := m.nextBufferFile()
	if filename != "" {
		var err error
		file, err = os.OpenFile(filename, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
		if err != nil {
			if file != nil {
				f := file
				file = nil

				f.Close()
			}
		}
	}

	m.bufFile = &bufferFile{
		file: file,
	}
}

func (m *inManager) completedFile(bf *bufferFile) {
	defer recover()

	if bf == nil {
		return
	}

	file := bf.file
	if file == nil {
		return
	}
	bf.file = nil

	defer func() {
		defer func(filename string) {
			defer recover()

			if exists, err := pathExists(m.outputDir); !exists || err != nil {
				os.MkdirAll(m.outputDir, 0777)
			}

			newName := m.outputDir + filepath.Base(filename)
			os.Rename(filename, newName)
		}(file.Name())

		file.Close()
	}()

	lnBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lnBytes, math.MaxUint32)
	file.Write(lnBytes)
}

func (m *inManager) writeToBuffer(data []byte) {
	ln := len(data)
	if ln == 0 {
		return
	}

	defer recover()

	m.Lock()
	defer m.Unlock()

	m.prepareBuffer(ln)

	fi := m.bufFile
	if fi == nil {
		return
	}

	f := fi.file
	if f == nil {
		return
	}

	stamp := make([]byte, 4)

	binary.BigEndian.PutUint32(stamp, 0)
	f.Write(stamp)

	binary.BigEndian.PutUint32(stamp, uint32(ln))
	f.Write(stamp)

	f.Write(data)

	binary.BigEndian.PutUint32(stamp, 0)
	f.Write(stamp)

	fi.size += ln + 12
	fi.count++
}

func (m *inManager) processInputs() {
	completed := false
	completeSignal := m.completed

	defer func() {
		defer recover()

		recover()
		atomic.StoreInt32(&m.processing, 0)

		if m.logger != nil {
			m.logger.Println("Stopping 'IN' manager...")
		}

		if m.inputs != nil {
			for _, in := range m.inputs {
				func() {
					defer recover()
					in.Close()
				}()
			}
		}

		if !completed && completeSignal != nil {
			func() {
				defer recover()
				completeSignal <- true
			}()
		}
	}()

	if m.logger != nil {
		m.logger.Println("Starting 'IN' manager...")
	}

	if len(m.inputs) > 0 {
		for _, in := range m.inputs {
			if in.Enabled() {
				go in.Run()
			}
		}
	}

	for {
		select {
		case <-completeSignal:
			completed = true
		case <-m.inQ.Ready():
			if !completed && atomic.LoadInt32(&m.poppingQueue) == 0 {
				t := time.Now()
				if t.Sub(m.lastQueueTime) >= time.Second {
					time.Sleep(time.Millisecond)
					go m.processQueue()
				}
			}
		}

		if completed {
			return
		}
	}
}
