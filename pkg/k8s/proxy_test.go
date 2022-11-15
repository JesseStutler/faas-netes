package k8s

import (
	"fmt"
	"strings"
	"testing"

	corelister "k8s.io/client-go/listers/core/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type FakeLister struct {
}

func (f FakeLister) List(selector labels.Selector) (ret []*corev1.Endpoints, err error) {
	return nil, nil
}

func (f FakeLister) Endpoints(namespace string) corelister.EndpointsNamespaceLister {

	return FakeNSLister{}
}

type FakeNSLister struct {
}

func (f FakeNSLister) List(selector labels.Selector) (ret []*corev1.Endpoints, err error) {
	return nil, nil
}

func (f FakeNSLister) Get(name string) (*corev1.Endpoints, error) {

	// make sure that we only send the function name to the lister
	if strings.Contains(name, ".") {
		return nil, fmt.Errorf("can not look up function name with a dot!")
	}

	ep := corev1.Endpoints{
		Subsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{{IP: "127.0.0.1"}},
		}},
	}

	return &ep, nil
}

func Test_FunctionLookup(t *testing.T) {

	lister := FakeLister{}

	resolver := NewFunctionLookup("testDefault", "random", lister)

	cases := []struct {
		name     string
		funcName string
		expError string
		expUrl   string
	}{
		{
			name:     "function without namespace uses default namespace",
			funcName: "testfunc",
			expUrl:   "http://127.0.0.1:8080",
		},
		{
			name:     "function with namespace uses the given namespace",
			funcName: "testfunc.othernamespace",
			expUrl:   "http://127.0.0.1:8080",
		},
		{
			name:     "url parse errors are returned",
			funcName: "testfunc.kube-system",
			expError: "namespace not allowed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, err := resolver.Resolve(tc.funcName)
			if tc.expError == "" && err != nil {
				t.Fatalf("expected no error, got %s", err)
			}

			if tc.expError != "" && (err == nil || !strings.Contains(err.Error(), tc.expError)) {
				t.Fatalf("expected %s, got %s", tc.expError, err)
			}

			if url.String() != tc.expUrl {
				t.Fatalf("expected url %s, got %s", tc.expUrl, url.String())
			}
		})
	}
}

type LBTestLister struct {
}

func (l LBTestLister) List(selector labels.Selector) (ret []*corev1.Endpoints, err error) {
	return nil, nil
}

func (l LBTestLister) Endpoints(namespace string) corelister.EndpointsNamespaceLister {
	return LBTestNsLister{}
}

type LBTestNsLister struct {
}

func (l LBTestNsLister) List(selector labels.Selector) (ret []*corev1.Endpoints, err error) {
	return nil, nil
}

func (l LBTestNsLister) Get(name string) (*corev1.Endpoints, error) {

	// 3 fake endpoints here
	ep := corev1.Endpoints{
		Subsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{{IP: "192.168.0.1"}, {IP: "192.168.0.2"}, {IP: "192.168.0.3"}},
		}},
	}

	return &ep, nil
}

func Test_LoadBalance(t *testing.T) {
	lister := LBTestLister{}
	endpoints := []string{"192.168.0.1", "192.168.0.2", "192.168.0.3"}
	requestTracing := map[string]int{
		"192.168.0.1": 4,
		"192.168.0.2": 5,
		"192.168.0.3": 3,
	}
	nextPos := 2
	leastConnResolver := NewFunctionLookup("testDefault", "least_connections", lister)
	leastConnResolver.EndpointsIps = endpoints
	leastConnResolver.RequestsTracing = requestTracing

	roundRobinResolver := NewFunctionLookup("testDefault", "round_robin", lister)
	roundRobinResolver.EndpointsIps = endpoints
	roundRobinResolver.Next = nextPos
	roundRobinResolver.RequestsTracing = requestTracing

	cases := []struct {
		name       string
		funcName   string
		resolver   *FunctionLookup
		expectUrl  string
		expectNext int
	}{
		{
			name:       "Least Connections",
			funcName:   "testFunc",
			resolver:   leastConnResolver,
			expectUrl:  "http://192.168.0.3:8080",
			expectNext: 0,
		},
		{
			name:       "Round Robin",
			funcName:   "testFunc",
			resolver:   roundRobinResolver,
			expectUrl:  "http://192.168.0.3:8080",
			expectNext: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, err := tc.resolver.Resolve(tc.funcName)
			if err != nil {
				t.Fatalf("expected no error, but got %s", err.Error())
			}
			if url.String() != tc.expectUrl {
				t.Fatalf("expected url %s, got %s", tc.expectUrl, url.String())
			}
			if tc.resolver.Next != tc.expectNext {
				t.Fatalf("expected next position is %d, got %d", tc.expectNext, tc.resolver.Next)
			}
		})
	}
}
