// Copyright 2019, OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package observiqreceiver

import (
	"context"
	"fmt"
	"sync"

	observiq "github.com/observiq/carbon/agent"
	obsentry "github.com/observiq/carbon/entry"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenterror"
	"go.opentelemetry.io/collector/consumer"
	"go.uber.org/zap"
)

type observiqReceiver struct {
	startOnce sync.Once
	stopOnce  sync.Once
	done      chan struct{}
	wg        sync.WaitGroup

	agent    *observiq.LogAgent
	logsChan chan *obsentry.Entry
	consumer consumer.LogsConsumer
	logger   *zap.Logger
}

// Ensure this factory adheres to required interface
var _ component.LogsReceiver = (*observiqReceiver)(nil)

// Start tells the receiver to start
func (r *observiqReceiver) Start(ctx context.Context, host component.Host) error {
	err := componenterror.ErrAlreadyStarted
	r.startOnce.Do(func() {
		err = nil
		r.logger.Info("Starting observiq receiver")

		if obsErr := r.agent.Start(); obsErr != nil {
			err = fmt.Errorf("start observiq: %s", err)
		}

		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			for {
				select {
				case <-r.done:
					return
				case obsLog := <-r.logsChan:
					if consumeErr := r.consumer.ConsumeLogs(ctx, convert(obsLog)); consumeErr != nil {
						r.logger.Error("ConsumeLogs() error", zap.String("error", consumeErr.Error()))
					}
				}
			}
		}()
	})

	return err
}

// Shutdown is invoked during service shutdown
func (r *observiqReceiver) Shutdown(context.Context) error {
	r.stopOnce.Do(func() {
		r.logger.Info("Shutting down observiq receiver")
		close(r.done)
		r.wg.Wait()
	})
	return nil
}
