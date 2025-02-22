// Copyright (c) 2020 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package event

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/gardener/gardener/pkg/client/kubernetes/clientmap"
	"github.com/gardener/gardener/pkg/client/kubernetes/clientmap/keys"
	"github.com/gardener/gardener/pkg/controllermanager/apis/config"
	"github.com/gardener/gardener/pkg/controllerutils"
)

const (
	// ControllerName is the name of this controller.
	ControllerName = "event"
)

// Controller controls Events.
type Controller struct {
	gardenClient client.Client
	cfg          *config.EventControllerConfiguration
	log          logr.Logger

	reconciler     reconcile.Reconciler
	hasSyncedFuncs []cache.InformerSynced

	eventQueue             workqueue.RateLimitingInterface
	numberOfRunningWorkers int
	workerCh               chan int
}

// NewController instantiates a new event controller
func NewController(
	ctx context.Context,
	log logr.Logger,
	clientMap clientmap.ClientMap,
	cfg *config.EventControllerConfiguration,
) (
	*Controller,
	error,
) {
	log = log.WithName(ControllerName)

	gardenClient, err := clientMap.GetClient(ctx, keys.ForGarden())
	if err != nil {
		return nil, err
	}

	events := &metav1.PartialObjectMetadata{}
	events.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Event"))
	eventInformer, err := gardenClient.Cache().GetInformer(ctx, events)
	if err != nil {
		return nil, fmt.Errorf("failed to get Event Informer: %w", err)
	}

	controller := &Controller{
		gardenClient: gardenClient.Client(),
		cfg:          cfg,
		log:          log,

		reconciler: NewEventReconciler(gardenClient.Client(), cfg),

		eventQueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "event"),
		workerCh:   make(chan int),
	}

	eventInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueEvent,
	})

	controller.hasSyncedFuncs = append(controller.hasSyncedFuncs, eventInformer.HasSynced)

	return controller, nil
}

// Run runs the Controller
func (c *Controller) Run(ctx context.Context) {
	var waitGroup sync.WaitGroup

	if !cache.WaitForCacheSync(ctx.Done(), c.hasSyncedFuncs...) {
		c.log.Error(wait.ErrWaitTimeout, "Timed out waiting for caches to sync")
		return
	}

	go func() {
		for res := range c.workerCh {
			c.numberOfRunningWorkers += res
		}
	}()

	c.log.Info("Event controller initialized")

	for i := 0; i < *c.cfg.ConcurrentSyncs; i++ {
		controllerutils.CreateWorker(ctx, c.eventQueue, "Event", c.reconciler, &waitGroup, c.workerCh, controllerutils.WithLogger(c.log))
	}

	<-ctx.Done()
	c.eventQueue.ShutDown()

	for {
		if c.eventQueue.Len() == 0 && c.numberOfRunningWorkers == 0 {
			c.log.V(1).Info("No running Event worker and no items left in the queues. Terminating Event controller")
			break
		}
		c.log.V(1).Info("Waiting for Event workers to finish", "numberOfRunningWorkers", c.numberOfRunningWorkers, "queueLength", c.eventQueue.Len())
		time.Sleep(5 * time.Second)
	}

	waitGroup.Wait()
}
