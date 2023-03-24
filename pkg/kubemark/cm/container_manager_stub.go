/*
Copyright 2015 The Kubernetes Authors.

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

package cm

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	internalapi "k8s.io/cri-api/pkg/apis"
	"k8s.io/klog/v2"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"
	cmlibrary "k8s.io/kubernetes/pkg/kubelet/cm"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/config"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager/cache"
	"k8s.io/kubernetes/pkg/kubelet/status"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"
	"strconv"
)

type containerManagerStub struct {
	shouldResetExtendedResourceCapacity bool
	GpuNum                              int
}

var _ cmlibrary.ContainerManager = &containerManagerStub{}

func (cm *containerManagerStub) Start(_ *v1.Node, _ cmlibrary.ActivePodsFunc, _ config.SourcesReady, _ status.PodStatusProvider, _ internalapi.RuntimeService) error {
	klog.V(2).Infof("Starting stub container manager")
	return nil
}

func (cm *containerManagerStub) SystemCgroupsLimit() v1.ResourceList {
	return v1.ResourceList{}
}

func (cm *containerManagerStub) GetNodeConfig() cmlibrary.NodeConfig {
	return cmlibrary.NodeConfig{}
}

func (cm *containerManagerStub) GetMountedSubsystems() *cmlibrary.CgroupSubsystems {
	return &cmlibrary.CgroupSubsystems{}
}

func (cm *containerManagerStub) GetQOSContainersInfo() cmlibrary.QOSContainersInfo {
	return cmlibrary.QOSContainersInfo{}
}

func (cm *containerManagerStub) UpdateQOSCgroups() error {
	return nil
}

func (cm *containerManagerStub) Status() cmlibrary.Status {
	return cmlibrary.Status{}
}

func (cm *containerManagerStub) GetNodeAllocatableReservation() v1.ResourceList {
	return nil
}

func (cm *containerManagerStub) GetCapacity() v1.ResourceList {
	c := v1.ResourceList{
		v1.ResourceEphemeralStorage: *resource.NewQuantity(
			int64(0),
			resource.BinarySI),
	}
	return c
}

func (cm *containerManagerStub) GetPluginRegistrationHandler() cache.PluginHandler {
	return nil
}

func (cm *containerManagerStub) GetDevicePluginResourceCapacity() (v1.ResourceList, v1.ResourceList, []string) {
	gpuNum, _ := resource.ParseQuantity(strconv.Itoa(cm.GpuNum))
	devicesCapacity := v1.ResourceList{
		"nvidia.com/gpu": gpuNum,
	}
	devicesAllocatable := v1.ResourceList{
		"nvidia.com/gpu": gpuNum,
	}
	return devicesCapacity, devicesAllocatable, []string{}
}

func (cm *containerManagerStub) NewPodContainerManager() cmlibrary.PodContainerManager {
	return &podContainerManagerStub{}
}

func (cm *containerManagerStub) GetResources(pod *v1.Pod, container *v1.Container) (*kubecontainer.RunContainerOptions, error) {
	return &kubecontainer.RunContainerOptions{}, nil
}

func (cm *containerManagerStub) UpdatePluginResources(*schedulerframework.NodeInfo, *lifecycle.PodAdmitAttributes) error {
	return nil
}

func (cm *containerManagerStub) InternalContainerLifecycle() cmlibrary.InternalContainerLifecycle {
	return &internalContainerLifecycleImpl{cpumanager.NewFakeManager(), topologymanager.NewFakeManager()}
}

func (cm *containerManagerStub) GetPodCgroupRoot() string {
	return ""
}

func (cm *containerManagerStub) GetDevices(_, _ string) []*podresourcesapi.ContainerDevices {
	return nil
}

func (cm *containerManagerStub) ShouldResetExtendedResourceCapacity() bool {
	return cm.shouldResetExtendedResourceCapacity
}

func (cm *containerManagerStub) GetAllocateResourcesPodAdmitHandler() lifecycle.PodAdmitHandler {
	return topologymanager.NewFakeManager()
}

func (cm *containerManagerStub) UpdateAllocatedDevices() {
	return
}

func (cm *containerManagerStub) GetCPUs(_, _ string) []int64 {
	return nil
}

func NewStubContainerManager(gpuNum int) cmlibrary.ContainerManager {
	return &containerManagerStub{shouldResetExtendedResourceCapacity: false, GpuNum: gpuNum}
}

func NewStubContainerManagerWithExtendedResource(shouldResetExtendedResourceCapacity bool) cmlibrary.ContainerManager {
	return &containerManagerStub{shouldResetExtendedResourceCapacity: shouldResetExtendedResourceCapacity}
}
