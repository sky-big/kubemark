/*
Copyright 2014 The Kubernetes Authors.

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

package client

import (
	"context"
	"fmt"
	"log"
	"reflect"
	rt "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/features"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	clientset "k8s.io/client-go/kubernetes"
	featuregatetesting "k8s.io/component-base/featuregate/testing"

	"k8s.io/component-base/version"
	kubeapiservertesting "k8s.io/kubernetes/cmd/kube-apiserver/app/testing"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	"k8s.io/kubernetes/test/integration/framework"
	imageutils "k8s.io/kubernetes/test/utils/image"
)

func TestClient(t *testing.T) {
	result := kubeapiservertesting.StartTestServerOrDie(t, nil, []string{"--disable-admission-plugins", "ServiceAccount"}, framework.SharedEtcd())
	defer result.TearDownFn()

	client := clientset.NewForConfigOrDie(result.ClientConfig)

	info, err := client.Discovery().ServerVersion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e, a := version.Get(), *info; !reflect.DeepEqual(e, a) {
		t.Errorf("expected %#v, got %#v", e, a)
	}

	pods, err := client.CoreV1().Pods("default").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods.Items) != 0 {
		t.Errorf("expected no pods, got %#v", pods)
	}

	// get a validation error
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test",
			Namespace:    "default",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name: "test",
				},
			},
		},
	}

	got, err := client.CoreV1().Pods("default").Create(context.TODO(), pod, metav1.CreateOptions{})
	if err == nil {
		t.Fatalf("unexpected non-error: %v", got)
	}

	// get a created pod
	pod.Spec.Containers[0].Image = "an-image"
	got, err = client.CoreV1().Pods("default").Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name == "" {
		t.Errorf("unexpected empty pod Name %v", got)
	}

	// pod is shown, but not scheduled
	pods, err = client.CoreV1().Pods("default").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Errorf("expected one pod, got %#v", pods)
	}
	actual := pods.Items[0]
	if actual.Name != got.Name {
		t.Errorf("expected pod %#v, got %#v", got, actual)
	}
	if actual.Spec.NodeName != "" {
		t.Errorf("expected pod to be unscheduled, got %#v", actual)
	}
}

func TestAtomicPut(t *testing.T) {
	result := kubeapiservertesting.StartTestServerOrDie(t, nil, []string{"--disable-admission-plugins", "ServiceAccount"}, framework.SharedEtcd())
	defer result.TearDownFn()

	c := clientset.NewForConfigOrDie(result.ClientConfig)

	rcBody := v1.ReplicationController{
		TypeMeta: metav1.TypeMeta{
			APIVersion: c.CoreV1().RESTClient().APIVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "atomicrc",
			Namespace: "default",
			Labels: map[string]string{
				"name": "atomicrc",
			},
		},
		Spec: v1.ReplicationControllerSpec{
			Replicas: func(i int32) *int32 { return &i }(0),
			Selector: map[string]string{
				"foo": "bar",
			},
			Template: &v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"foo": "bar",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{Name: "name", Image: "image"},
					},
				},
			},
		},
	}
	rcs := c.CoreV1().ReplicationControllers("default")
	rc, err := rcs.Create(context.TODO(), &rcBody, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed creating atomicRC: %v", err)
	}
	testLabels := labels.Set{
		"foo": "bar",
	}
	for i := 0; i < 5; i++ {
		// a: z, b: y, etc...
		testLabels[string([]byte{byte('a' + i)})] = string([]byte{byte('z' - i)})
	}
	var wg sync.WaitGroup
	wg.Add(len(testLabels))
	for label, value := range testLabels {
		go func(l, v string) {
			defer wg.Done()
			for {
				tmpRC, err := rcs.Get(context.TODO(), rc.Name, metav1.GetOptions{})
				if err != nil {
					t.Errorf("Error getting atomicRC: %v", err)
					continue
				}
				if tmpRC.Spec.Selector == nil {
					tmpRC.Spec.Selector = map[string]string{l: v}
					tmpRC.Spec.Template.Labels = map[string]string{l: v}
				} else {
					tmpRC.Spec.Selector[l] = v
					tmpRC.Spec.Template.Labels[l] = v
				}
				_, err = rcs.Update(context.TODO(), tmpRC, metav1.UpdateOptions{})
				if err != nil {
					if apierrors.IsConflict(err) {
						// This is what we expect.
						continue
					}
					t.Errorf("Unexpected error putting atomicRC: %v", err)
					continue
				}
				return
			}
		}(label, value)
	}
	wg.Wait()
	rc, err = rcs.Get(context.TODO(), rc.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed getting atomicRC after writers are complete: %v", err)
	}
	if !reflect.DeepEqual(testLabels, labels.Set(rc.Spec.Selector)) {
		t.Errorf("Selector PUTs were not atomic: wanted %v, got %v", testLabels, rc.Spec.Selector)
	}
}

func TestPatch(t *testing.T) {
	result := kubeapiservertesting.StartTestServerOrDie(t, nil, []string{"--disable-admission-plugins", "ServiceAccount"}, framework.SharedEtcd())
	defer result.TearDownFn()

	c := clientset.NewForConfigOrDie(result.ClientConfig)

	name := "patchpod"
	resource := "pods"
	podBody := v1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: c.CoreV1().RESTClient().APIVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "name", Image: "image"},
			},
		},
	}
	pods := c.CoreV1().Pods("default")
	_, err := pods.Create(context.TODO(), &podBody, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed creating patchpods: %v", err)
	}

	patchBodies := map[schema.GroupVersion]map[types.PatchType]struct {
		AddLabelBody        []byte
		RemoveLabelBody     []byte
		RemoveAllLabelsBody []byte
	}{
		v1.SchemeGroupVersion: {
			types.JSONPatchType: {
				[]byte(`[{"op":"add","path":"/metadata/labels","value":{"foo":"bar","baz":"qux"}}]`),
				[]byte(`[{"op":"remove","path":"/metadata/labels/foo"}]`),
				[]byte(`[{"op":"remove","path":"/metadata/labels"}]`),
			},
			types.MergePatchType: {
				[]byte(`{"metadata":{"labels":{"foo":"bar","baz":"qux"}}}`),
				[]byte(`{"metadata":{"labels":{"foo":null}}}`),
				[]byte(`{"metadata":{"labels":null}}`),
			},
			types.StrategicMergePatchType: {
				[]byte(`{"metadata":{"labels":{"foo":"bar","baz":"qux"}}}`),
				[]byte(`{"metadata":{"labels":{"foo":null}}}`),
				[]byte(`{"metadata":{"labels":{"$patch":"replace"}}}`),
			},
		},
	}

	pb := patchBodies[c.CoreV1().RESTClient().APIVersion()]

	execPatch := func(pt types.PatchType, body []byte) error {
		result := c.CoreV1().RESTClient().Patch(pt).
			Resource(resource).
			Namespace("default").
			Name(name).
			Body(body).
			Do(context.TODO())
		if result.Error() != nil {
			return result.Error()
		}

		// trying to chase flakes, this should give us resource versions of objects as we step through
		jsonObj, err := result.Raw()
		if err != nil {
			t.Log(err)
		} else {
			t.Logf("%v", string(jsonObj))
		}

		return nil
	}

	for k, v := range pb {
		// add label
		err := execPatch(k, v.AddLabelBody)
		if err != nil {
			t.Fatalf("Failed updating patchpod with patch type %s: %v", k, err)
		}
		pod, err := pods.Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Failed getting patchpod: %v", err)
		}
		if len(pod.Labels) != 2 || pod.Labels["foo"] != "bar" || pod.Labels["baz"] != "qux" {
			t.Errorf("Failed updating patchpod with patch type %s: labels are: %v", k, pod.Labels)
		}

		// remove one label
		err = execPatch(k, v.RemoveLabelBody)
		if err != nil {
			t.Fatalf("Failed updating patchpod with patch type %s: %v", k, err)
		}
		pod, err = pods.Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Failed getting patchpod: %v", err)
		}
		if len(pod.Labels) != 1 || pod.Labels["baz"] != "qux" {
			t.Errorf("Failed updating patchpod with patch type %s: labels are: %v", k, pod.Labels)
		}

		// remove all labels
		err = execPatch(k, v.RemoveAllLabelsBody)
		if err != nil {
			t.Fatalf("Failed updating patchpod with patch type %s: %v", k, err)
		}
		pod, err = pods.Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("Failed getting patchpod: %v", err)
		}
		if pod.Labels != nil {
			t.Errorf("Failed remove all labels from patchpod with patch type %s: %v", k, pod.Labels)
		}
	}
}

func TestPatchWithCreateOnUpdate(t *testing.T) {
	result := kubeapiservertesting.StartTestServerOrDie(t, nil, nil, framework.SharedEtcd())
	defer result.TearDownFn()

	c := clientset.NewForConfigOrDie(result.ClientConfig)

	endpointTemplate := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "patchendpoint",
			Namespace: "default",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []v1.EndpointPort{{Port: 80, Protocol: v1.ProtocolTCP}},
			},
		},
	}

	patchEndpoint := func(json []byte) (runtime.Object, error) {
		return c.CoreV1().RESTClient().Patch(types.MergePatchType).Resource("endpoints").Namespace("default").Name("patchendpoint").Body(json).Do(context.TODO()).Get()
	}

	// Make sure patch doesn't get to CreateOnUpdate
	{
		endpointJSON, err := runtime.Encode(legacyscheme.Codecs.LegacyCodec(v1.SchemeGroupVersion), endpointTemplate)
		if err != nil {
			t.Fatalf("Failed creating endpoint JSON: %v", err)
		}
		if obj, err := patchEndpoint(endpointJSON); !apierrors.IsNotFound(err) {
			t.Errorf("Expected notfound creating from patch, got error=%v and object: %#v", err, obj)
		}
	}

	// Create the endpoint (endpoints set AllowCreateOnUpdate=true) to get a UID and resource version
	createdEndpoint, err := c.CoreV1().Endpoints("default").Update(context.TODO(), endpointTemplate, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("Failed creating endpoint: %v", err)
	}

	// Make sure identity patch is accepted
	{
		endpointJSON, err := runtime.Encode(legacyscheme.Codecs.LegacyCodec(v1.SchemeGroupVersion), createdEndpoint)
		if err != nil {
			t.Fatalf("Failed creating endpoint JSON: %v", err)
		}
		if _, err := patchEndpoint(endpointJSON); err != nil {
			t.Errorf("Failed patching endpoint: %v", err)
		}
	}

	// Make sure patch complains about a mismatched resourceVersion
	{
		endpointTemplate.Name = ""
		endpointTemplate.UID = ""
		endpointTemplate.ResourceVersion = "1"
		endpointJSON, err := runtime.Encode(legacyscheme.Codecs.LegacyCodec(v1.SchemeGroupVersion), endpointTemplate)
		if err != nil {
			t.Fatalf("Failed creating endpoint JSON: %v", err)
		}
		if _, err := patchEndpoint(endpointJSON); !apierrors.IsConflict(err) {
			t.Errorf("Expected error, got %#v", err)
		}
	}

	// Make sure patch complains about mutating the UID
	{
		endpointTemplate.Name = ""
		endpointTemplate.UID = "abc"
		endpointTemplate.ResourceVersion = ""
		endpointJSON, err := runtime.Encode(legacyscheme.Codecs.LegacyCodec(v1.SchemeGroupVersion), endpointTemplate)
		if err != nil {
			t.Fatalf("Failed creating endpoint JSON: %v", err)
		}
		if _, err := patchEndpoint(endpointJSON); !apierrors.IsInvalid(err) {
			t.Errorf("Expected error, got %#v", err)
		}
	}

	// Make sure patch complains about a mismatched name
	{
		endpointTemplate.Name = "changedname"
		endpointTemplate.UID = ""
		endpointTemplate.ResourceVersion = ""
		endpointJSON, err := runtime.Encode(legacyscheme.Codecs.LegacyCodec(v1.SchemeGroupVersion), endpointTemplate)
		if err != nil {
			t.Fatalf("Failed creating endpoint JSON: %v", err)
		}
		if _, err := patchEndpoint(endpointJSON); !apierrors.IsBadRequest(err) {
			t.Errorf("Expected error, got %#v", err)
		}
	}

	// Make sure patch containing originally submitted JSON is accepted
	{
		endpointTemplate.Name = ""
		endpointTemplate.UID = ""
		endpointTemplate.ResourceVersion = ""
		endpointJSON, err := runtime.Encode(legacyscheme.Codecs.LegacyCodec(v1.SchemeGroupVersion), endpointTemplate)
		if err != nil {
			t.Fatalf("Failed creating endpoint JSON: %v", err)
		}
		if _, err := patchEndpoint(endpointJSON); err != nil {
			t.Errorf("Failed patching endpoint: %v", err)
		}
	}
}

func TestAPIVersions(t *testing.T) {
	result := kubeapiservertesting.StartTestServerOrDie(t, nil, nil, framework.SharedEtcd())
	defer result.TearDownFn()

	c := clientset.NewForConfigOrDie(result.ClientConfig)

	clientVersion := c.CoreV1().RESTClient().APIVersion().String()
	g, err := c.Discovery().ServerGroups()
	if err != nil {
		t.Fatalf("Failed to get api versions: %v", err)
	}
	versions := metav1.ExtractGroupVersions(g)

	// Verify that the server supports the API version used by the client.
	for _, version := range versions {
		if version == clientVersion {
			return
		}
	}
	t.Errorf("Server does not support APIVersion used by client. Server supported APIVersions: '%v', client APIVersion: '%v'", versions, clientVersion)
}

func TestEventValidation(t *testing.T) {
	result := kubeapiservertesting.StartTestServerOrDie(t, nil, nil, framework.SharedEtcd())
	defer result.TearDownFn()

	client := clientset.NewForConfigOrDie(result.ClientConfig)

	createNamespace := func(namespace string) string {
		if namespace == "" {
			namespace = metav1.NamespaceDefault
		}
		return namespace
	}

	mkCoreEvent := func(ver string, ns string) *v1.Event {
		name := fmt.Sprintf("%v-%v-event", ver, ns)
		namespace := createNamespace(ns)
		return &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
			InvolvedObject: v1.ObjectReference{
				Namespace: ns,
				Name:      name,
			},
			Count:               2,
			Type:                "Normal",
			ReportingController: "test-controller",
			ReportingInstance:   "test-1",
			Reason:              fmt.Sprintf("event %v test", name),
			Action:              "Testing",
		}
	}
	mkV1Event := func(ver string, ns string) *eventsv1.Event {
		name := fmt.Sprintf("%v-%v-event", ver, ns)
		namespace := createNamespace(ns)
		return &eventsv1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
			Regarding: v1.ObjectReference{
				Namespace: ns,
				Name:      name,
			},
			Series: &eventsv1.EventSeries{
				Count:            2,
				LastObservedTime: metav1.MicroTime{Time: time.Now()},
			},
			Type:                "Normal",
			EventTime:           metav1.MicroTime{Time: time.Now()},
			ReportingController: "test-controller",
			ReportingInstance:   "test-2",
			Reason:              fmt.Sprintf("event %v test", name),
			Action:              "Testing",
		}
	}

	testcases := []struct {
		name      string
		namespace string
		hasError  bool
	}{
		{
			name:      "Involved object is namespaced",
			namespace: "kube-system",
			hasError:  false,
		},
		{
			name:      "Involved object is cluster-scoped",
			namespace: "",
			hasError:  false,
		},
	}

	for _, test := range testcases {
		// create test
		oldEventObj := mkCoreEvent("corev1", test.namespace)
		corev1Event, err := client.CoreV1().Events(oldEventObj.Namespace).Create(context.TODO(), oldEventObj, metav1.CreateOptions{})
		if err != nil && !test.hasError {
			t.Errorf("%v, call Create failed, expect has error: %v, but got: %v", test.name, test.hasError, err)
		}
		newEventObj := mkV1Event("eventsv1", test.namespace)
		eventsv1Event, err := client.EventsV1().Events(newEventObj.Namespace).Create(context.TODO(), newEventObj, metav1.CreateOptions{})
		if err != nil && !test.hasError {
			t.Errorf("%v, call Create failed, expect has error: %v, but got: %v", test.name, test.hasError, err)
		}
		if corev1Event.Namespace != eventsv1Event.Namespace {
			t.Errorf("%v, events created by different api client have different namespaces that isn't expected", test.name)
		}
		// update test
		corev1Event.Count++
		corev1Event, err = client.CoreV1().Events(corev1Event.Namespace).Update(context.TODO(), corev1Event, metav1.UpdateOptions{})
		if err != nil && !test.hasError {
			t.Errorf("%v, call Update failed, expect has error: %v, but got: %v", test.name, test.hasError, err)
		}
		eventsv1Event.Series.Count++
		eventsv1Event.Series.LastObservedTime = metav1.MicroTime{Time: time.Now()}
		eventsv1Event, err = client.EventsV1().Events(eventsv1Event.Namespace).Update(context.TODO(), eventsv1Event, metav1.UpdateOptions{})
		if err != nil && !test.hasError {
			t.Errorf("%v, call Update failed, expect has error: %v, but got: %v", test.name, test.hasError, err)
		}
		if corev1Event.Namespace != eventsv1Event.Namespace {
			t.Errorf("%v, events updated by different api client have different namespaces that isn't expected", test.name)
		}
	}
}

func TestEventCompatibility(t *testing.T) {
	result := kubeapiservertesting.StartTestServerOrDie(t, nil, nil, framework.SharedEtcd())
	defer result.TearDownFn()

	client := clientset.NewForConfigOrDie(result.ClientConfig)

	coreevents := []*v1.Event{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pass-core-default-cluster-scoped",
				Namespace: "default",
			},
			Type:                "Normal",
			Reason:              "event test",
			Action:              "Testing",
			ReportingController: "test-controller",
			ReportingInstance:   "test-controller-1",
			InvolvedObject:      v1.ObjectReference{Kind: "Node", Name: "foo", Namespace: ""},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fail-core-kube-system-cluster-scoped",
				Namespace: "kube-system",
			},
			Type:                "Normal",
			Reason:              "event test",
			Action:              "Testing",
			ReportingController: "test-controller",
			ReportingInstance:   "test-controller-1",
			InvolvedObject:      v1.ObjectReference{Kind: "Node", Name: "foo", Namespace: ""},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fail-core-other-ns-cluster-scoped",
				Namespace: "test-ns",
			},
			Type:                "Normal",
			Reason:              "event test",
			Action:              "Testing",
			ReportingController: "test-controller",
			ReportingInstance:   "test-controller-1",
			InvolvedObject:      v1.ObjectReference{Kind: "Node", Name: "foo", Namespace: ""},
		},
	}
	for _, e := range coreevents {
		t.Run(e.Name, func(t *testing.T) {
			_, err := client.CoreV1().Events(e.Namespace).Create(context.TODO(), e, metav1.CreateOptions{})
			if err == nil && !strings.HasPrefix(e.Name, "pass-") {
				t.Fatalf("unexpected pass")
			}
			if err != nil && !strings.HasPrefix(e.Name, "fail-") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	v1events := []*eventsv1.Event{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pass-events-default-cluster-scoped",
				Namespace: "default",
			},
			EventTime:           metav1.MicroTime{Time: time.Now()},
			Type:                "Normal",
			Reason:              "event test",
			Action:              "Testing",
			ReportingController: "test-controller",
			ReportingInstance:   "test-controller-1",
			Regarding:           v1.ObjectReference{Kind: "Node", Name: "foo", Namespace: ""},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pass-events-kube-system-cluster-scoped",
				Namespace: "kube-system",
			},
			EventTime:           metav1.MicroTime{Time: time.Now()},
			Type:                "Normal",
			Reason:              "event test",
			Action:              "Testing",
			ReportingController: "test-controller",
			ReportingInstance:   "test-controller-1",
			Regarding:           v1.ObjectReference{Kind: "Node", Name: "foo", Namespace: ""},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "fail-events-other-ns-cluster-scoped",
				Namespace: "test-ns",
			},
			EventTime:           metav1.MicroTime{Time: time.Now()},
			Type:                "Normal",
			Reason:              "event test",
			Action:              "Testing",
			ReportingController: "test-controller",
			ReportingInstance:   "test-controller-1",
			Regarding:           v1.ObjectReference{Kind: "Node", Name: "foo", Namespace: ""},
		},
	}
	for _, e := range v1events {
		t.Run(e.Name, func(t *testing.T) {
			_, err := client.EventsV1().Events(e.Namespace).Create(context.TODO(), e, metav1.CreateOptions{})
			if err == nil && !strings.HasPrefix(e.Name, "pass-") {
				t.Fatalf("unexpected pass")
			}
			if err != nil && !strings.HasPrefix(e.Name, "fail-") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSingleWatch(t *testing.T) {
	result := kubeapiservertesting.StartTestServerOrDie(t, nil, nil, framework.SharedEtcd())
	defer result.TearDownFn()

	client := clientset.NewForConfigOrDie(result.ClientConfig)

	mkEvent := func(i int) *v1.Event {
		name := fmt.Sprintf("event-%v", i)
		return &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      name,
			},
			InvolvedObject: v1.ObjectReference{
				Namespace: "default",
				Name:      name,
			},
			Reason: fmt.Sprintf("event %v", i),
		}
	}

	rv1 := ""
	for i := 0; i < 10; i++ {
		event := mkEvent(i)
		got, err := client.CoreV1().Events("default").Create(context.TODO(), event, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("Failed creating event %#q: %v", event, err)
		}
		if rv1 == "" {
			rv1 = got.ResourceVersion
			if rv1 == "" {
				t.Fatal("did not get a resource version.")
			}
		}
		t.Logf("Created event %#v", got.ObjectMeta)
	}

	w, err := client.CoreV1().RESTClient().Get().
		Namespace("default").
		Resource("events").
		VersionedParams(&metav1.ListOptions{
			ResourceVersion: rv1,
			Watch:           true,
			FieldSelector:   fields.OneTermEqualSelector("metadata.name", "event-9").String(),
		}, metav1.ParameterCodec).
		Watch(context.TODO())

	if err != nil {
		t.Fatalf("Failed watch: %v", err)
	}
	defer w.Stop()

	select {
	case <-time.After(wait.ForeverTestTimeout):
		t.Fatalf("watch took longer than %s", wait.ForeverTestTimeout.String())
	case got, ok := <-w.ResultChan():
		if !ok {
			t.Fatal("Watch channel closed unexpectedly.")
		}

		// We expect to see an ADD of event-9 and only event-9. (This
		// catches a bug where all the events would have been sent down
		// the channel.)
		if e, a := watch.Added, got.Type; e != a {
			t.Errorf("Wanted %v, got %v", e, a)
		}
		switch o := got.Object.(type) {
		case *v1.Event:
			if e, a := "event-9", o.Name; e != a {
				t.Errorf("Wanted %v, got %v", e, a)
			}
		default:
			t.Fatalf("Unexpected watch event containing object %#q", got)
		}
	}
}

func TestMultiWatch(t *testing.T) {
	// Disable this test as long as it demonstrates a problem.
	// TODO: Re-enable this test when we get #6059 resolved.
	t.Skip()

	const watcherCount = 50
	rt.GOMAXPROCS(watcherCount)

	result := kubeapiservertesting.StartTestServerOrDie(t, nil, nil, framework.SharedEtcd())
	defer result.TearDownFn()

	client := clientset.NewForConfigOrDie(result.ClientConfig)

	dummyEvent := func(i int) *v1.Event {
		name := fmt.Sprintf("unrelated-%v", i)
		return &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%v.%x", name, time.Now().UnixNano()),
				Namespace: "default",
			},
			InvolvedObject: v1.ObjectReference{
				Name:      name,
				Namespace: "default",
			},
			Reason: fmt.Sprintf("unrelated change %v", i),
		}
	}

	type timePair struct {
		t    time.Time
		name string
	}

	receivedTimes := make(chan timePair, watcherCount*2)
	watchesStarted := sync.WaitGroup{}

	// make a bunch of pods and watch them
	for i := 0; i < watcherCount; i++ {
		watchesStarted.Add(1)
		name := fmt.Sprintf("multi-watch-%v", i)
		got, err := client.CoreV1().Pods("default").Create(context.TODO(), &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: labels.Set{"watchlabel": name},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{{
					Name:  "pause",
					Image: imageutils.GetPauseImageName(),
				}},
			},
		}, metav1.CreateOptions{})

		if err != nil {
			t.Fatalf("Couldn't make %v: %v", name, err)
		}
		go func(name, rv string) {
			options := metav1.ListOptions{
				LabelSelector:   labels.Set{"watchlabel": name}.AsSelector().String(),
				ResourceVersion: rv,
			}
			w, err := client.CoreV1().Pods("default").Watch(context.TODO(), options)
			if err != nil {
				panic(fmt.Sprintf("watch error for %v: %v", name, err))
			}
			defer w.Stop()
			watchesStarted.Done()
			e, ok := <-w.ResultChan() // should get the update (that we'll do below)
			if !ok {
				panic(fmt.Sprintf("%v ended early?", name))
			}
			if e.Type != watch.Modified {
				panic(fmt.Sprintf("Got unexpected watch notification:\n%v: %+v %+v", name, e, e.Object))
			}
			receivedTimes <- timePair{time.Now(), name}
		}(name, got.ObjectMeta.ResourceVersion)
	}
	log.Printf("%v: %v pods made and watchers started", time.Now(), watcherCount)

	// wait for watches to start before we start spamming the system with
	// objects below, otherwise we'll hit the watch window restriction.
	watchesStarted.Wait()

	const (
		useEventsAsUnrelatedType = false
		usePodsAsUnrelatedType   = true
	)

	// make a bunch of unrelated changes in parallel
	if useEventsAsUnrelatedType {
		const unrelatedCount = 3000
		var wg sync.WaitGroup
		defer wg.Wait()
		changeToMake := make(chan int, unrelatedCount*2)
		changeMade := make(chan int, unrelatedCount*2)
		go func() {
			for i := 0; i < unrelatedCount; i++ {
				changeToMake <- i
			}
			close(changeToMake)
		}()
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i, ok := <-changeToMake
					if !ok {
						return
					}
					if _, err := client.CoreV1().Events("default").Create(context.TODO(), dummyEvent(i), metav1.CreateOptions{}); err != nil {
						panic(fmt.Sprintf("couldn't make an event: %v", err))
					}
					changeMade <- i
				}
			}()
		}

		for i := 0; i < 2000; i++ {
			<-changeMade
			if (i+1)%50 == 0 {
				log.Printf("%v: %v unrelated changes made", time.Now(), i+1)
			}
		}
	}
	if usePodsAsUnrelatedType {
		const unrelatedCount = 3000
		var wg sync.WaitGroup
		defer wg.Wait()
		changeToMake := make(chan int, unrelatedCount*2)
		changeMade := make(chan int, unrelatedCount*2)
		go func() {
			for i := 0; i < unrelatedCount; i++ {
				changeToMake <- i
			}
			close(changeToMake)
		}()
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i, ok := <-changeToMake
					if !ok {
						return
					}
					name := fmt.Sprintf("unrelated-%v", i)
					_, err := client.CoreV1().Pods("default").Create(context.TODO(), &v1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: name,
						},
						Spec: v1.PodSpec{
							Containers: []v1.Container{{
								Name:  "nothing",
								Image: imageutils.GetPauseImageName(),
							}},
						},
					}, metav1.CreateOptions{})

					if err != nil {
						panic(fmt.Sprintf("couldn't make unrelated pod: %v", err))
					}
					changeMade <- i
				}
			}()
		}

		for i := 0; i < 2000; i++ {
			<-changeMade
			if (i+1)%50 == 0 {
				log.Printf("%v: %v unrelated changes made", time.Now(), i+1)
			}
		}
	}

	// Now we still have changes being made in parallel, but at least 1000 have been made.
	// Make some updates to send down the watches.
	sentTimes := make(chan timePair, watcherCount*2)
	for i := 0; i < watcherCount; i++ {
		go func(i int) {
			name := fmt.Sprintf("multi-watch-%v", i)
			pod, err := client.CoreV1().Pods("default").Get(context.TODO(), name, metav1.GetOptions{})
			if err != nil {
				panic(fmt.Sprintf("Couldn't get %v: %v", name, err))
			}
			pod.Spec.Containers[0].Image = imageutils.GetPauseImageName()
			sentTimes <- timePair{time.Now(), name}
			if _, err := client.CoreV1().Pods("default").Update(context.TODO(), pod, metav1.UpdateOptions{}); err != nil {
				panic(fmt.Sprintf("Couldn't make %v: %v", name, err))
			}
		}(i)
	}

	sent := map[string]time.Time{}
	for i := 0; i < watcherCount; i++ {
		tp := <-sentTimes
		sent[tp.name] = tp.t
	}
	log.Printf("all changes made")
	dur := map[string]time.Duration{}
	for i := 0; i < watcherCount; i++ {
		tp := <-receivedTimes
		delta := tp.t.Sub(sent[tp.name])
		dur[tp.name] = delta
		log.Printf("%v: %v", tp.name, delta)
	}
	log.Printf("all watches ended")
	t.Errorf("durations: %v", dur)
}

func runSelfLinkTestOnNamespace(t *testing.T, c clientset.Interface, namespace string) {
	podBody := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "selflinktest",
			Namespace: namespace,
			Labels: map[string]string{
				"name": "selflinktest",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "name", Image: "image"},
			},
		},
	}
	pod, err := c.CoreV1().Pods(namespace).Create(context.TODO(), &podBody, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed creating selflinktest pod: %v", err)
	}
	if err = c.CoreV1().RESTClient().Get().RequestURI(pod.SelfLink).Do(context.TODO()).Into(pod); err != nil {
		t.Errorf("Failed listing pod with supplied self link '%v': %v", pod.SelfLink, err)
	}

	podList, err := c.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		t.Errorf("Failed listing pods: %v", err)
	}

	if err = c.CoreV1().RESTClient().Get().RequestURI(podList.SelfLink).Do(context.TODO()).Into(podList); err != nil {
		t.Errorf("Failed listing pods with supplied self link '%v': %v", podList.SelfLink, err)
	}

	found := false
	for i := range podList.Items {
		item := &podList.Items[i]
		if item.Name != "selflinktest" {
			continue
		}
		found = true
		err = c.CoreV1().RESTClient().Get().RequestURI(item.SelfLink).Do(context.TODO()).Into(pod)
		if err != nil {
			t.Errorf("Failed listing pod with supplied self link '%v': %v", item.SelfLink, err)
		}
		break
	}
	if !found {
		t.Errorf("never found selflinktest pod in namespace %s", namespace)
	}
}

func TestSelfLinkOnNamespace(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.RemoveSelfLink, false)()

	result := kubeapiservertesting.StartTestServerOrDie(t, nil, []string{"--disable-admission-plugins", "ServiceAccount"}, framework.SharedEtcd())
	defer result.TearDownFn()

	c := clientset.NewForConfigOrDie(result.ClientConfig)

	runSelfLinkTestOnNamespace(t, c, "default")
}