// Copyright (c) 2019 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package controller

import (
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Shoot", func() {
	trueVar := true
	falseVar := false

	cidr := "10.250.0.0/19"

	DescribeTable("#GetPodNetwork",
		func(cluster *Cluster, cidr string) {
			Expect(GetPodNetwork(cluster)).To(Equal(cidr))
		},

		Entry("pod cidr is given", &Cluster{
			Shoot: &gardencorev1beta1.Shoot{
				Spec: gardencorev1beta1.ShootSpec{
					Networking: gardencorev1beta1.Networking{
						Pods: &cidr,
					},
				},
			},
		}, cidr),
	)

	DescribeTable("#IsHibernated",
		func(hibernation *gardencorev1beta1.Hibernation, expectation bool) {
			cluster := &Cluster{
				Shoot: &gardencorev1beta1.Shoot{
					Spec: gardencorev1beta1.ShootSpec{
						Hibernation: hibernation,
					},
				},
			}

			Expect(IsHibernated(cluster)).To(Equal(expectation))
		},

		Entry("hibernation is nil", nil, false),
		Entry("hibernation is not enabled", &gardencorev1beta1.Hibernation{Enabled: &falseVar}, false),
		Entry("hibernation is enabled", &gardencorev1beta1.Hibernation{Enabled: &trueVar}, true),
	)

	var (
		dnsDomain            = "dnsdomain"
		dnsProviderType      = "type"
		dnsProviderUnmanaged = "unmanaged"
	)

	DescribeTable("#IsUnmanagedDNSProvider",
		func(dns *gardencorev1beta1.DNS, expectation bool) {
			cluster := &Cluster{
				Shoot: &gardencorev1beta1.Shoot{
					Spec: gardencorev1beta1.ShootSpec{
						DNS: dns,
					},
				},
			}

			Expect(IsUnmanagedDNSProvider(cluster)).To(Equal(expectation))
		},

		Entry("dns is nil", nil, true),
		Entry("dns domain is set", &gardencorev1beta1.DNS{
			Domain: &dnsDomain,
		}, false),
		Entry("dns domain is not set and provider is not given", &gardencorev1beta1.DNS{
			Providers: []gardencorev1beta1.DNSProvider{},
		}, false),
		Entry("dns domain is not set and provider is given but type is not unmanaged", &gardencorev1beta1.DNS{
			Providers: []gardencorev1beta1.DNSProvider{{
				Type: &dnsProviderType,
			}},
		}, false),
		Entry("dns domain is not set and provider is given and type is unmanaged", &gardencorev1beta1.DNS{
			Providers: []gardencorev1beta1.DNSProvider{{
				Type: &dnsProviderUnmanaged,
			}},
		}, true),
	)

	DescribeTable("#GetReplicas",
		func(hibernation *gardencorev1beta1.Hibernation, wokenUp, expectation int) {
			cluster := &Cluster{
				Shoot: &gardencorev1beta1.Shoot{
					Spec: gardencorev1beta1.ShootSpec{
						Hibernation: hibernation,
					},
				},
			}

			Expect(GetReplicas(cluster, wokenUp)).To(Equal(expectation))
		},

		Entry("hibernation is not enabled", nil, 3, 3),
		Entry("hibernation is enabled", &gardencorev1beta1.Hibernation{Enabled: &trueVar}, 1, 0),
	)

	DescribeTable("#IsFailed",
		func(lastOperation *gardencorev1beta1.LastOperation, expectedToBeFailed bool) {
			cluster := &Cluster{
				Shoot: &gardencorev1beta1.Shoot{
					Status: gardencorev1beta1.ShootStatus{
						LastOperation: lastOperation,
					},
				},
			}

			Expect(IsFailed(cluster)).To(Equal(expectedToBeFailed))
		},

		Entry("cluster is failed", &gardencorev1beta1.LastOperation{State: gardencorev1beta1.LastOperationStateFailed}, true),
		Entry("cluster is not failed", &gardencorev1beta1.LastOperation{State: gardencorev1beta1.LastOperationStateError}, false),
		Entry("cluster is not failed", nil, false),
	)

	DescribeTable("#IsShootFailed",
		func(lastOperation *gardencorev1beta1.LastOperation, expectedToBeFailed bool) {
			shoot := &gardencorev1beta1.Shoot{
				Status: gardencorev1beta1.ShootStatus{
					LastOperation: lastOperation,
				},
			}
			Expect(IsShootFailed(shoot)).To(Equal(expectedToBeFailed))
		},

		Entry("cluster is failed", &gardencorev1beta1.LastOperation{State: gardencorev1beta1.LastOperationStateFailed}, true),
		Entry("cluster is not failed", &gardencorev1beta1.LastOperation{State: gardencorev1beta1.LastOperationStateError}, false),
		Entry("cluster is not failed", nil, false),
	)
})
