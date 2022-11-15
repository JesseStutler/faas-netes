// Copyright (c) Alex Ellis 2017. All rights reserved.
// Copyright 2020 OpenFaaS Author(s)
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package k8s

import (
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"sync"

	corelister "k8s.io/client-go/listers/core/v1"
)

// watchdogPort for the OpenFaaS function watchdog
const watchdogPort = 8080

func NewFunctionLookup(ns string, lbType string, lister corelister.EndpointsLister) *FunctionLookup {
	return &FunctionLookup{
		DefaultNamespace:  ns,
		EndpointLister:    lister,
		Listers:           map[string]corelister.EndpointsNamespaceLister{},
		LoadBalancingType: lbType,
		RequestsTracing:   map[string]int{},
		EndpointsIps:      make([]string, 0, 5),
		Next:              0,
		Lock:              sync.RWMutex{},
	}
}

type FunctionLookup struct {
	DefaultNamespace  string
	EndpointLister    corelister.EndpointsLister
	Listers           map[string]corelister.EndpointsNamespaceLister
	LoadBalancingType string         //Least Connections, Random, Round Robin...
	RequestsTracing   map[string]int //key为endpoint ip，value为in flight请求数量
	EndpointsIps      []string
	Next              int //用于round robin，记录下一次应该请求的EndpointsIps位置
	Lock              sync.RWMutex
}

func (f *FunctionLookup) GetLister(ns string) corelister.EndpointsNamespaceLister {
	f.Lock.RLock()
	defer f.Lock.RUnlock()
	return f.Listers[ns]
}

func (f *FunctionLookup) SetLister(ns string, lister corelister.EndpointsNamespaceLister) {
	f.Lock.Lock()
	defer f.Lock.Unlock()
	f.Listers[ns] = lister
}

func getNamespace(name, defaultNamespace string) string {
	namespace := defaultNamespace
	if strings.Contains(name, ".") {
		namespace = name[strings.LastIndexAny(name, ".")+1:]
	}
	return namespace
}

func (l *FunctionLookup) Resolve(name string) (url.URL, error) {
	functionName := name
	namespace := getNamespace(name, l.DefaultNamespace)
	if err := l.verifyNamespace(namespace); err != nil {
		return url.URL{}, err
	}

	if strings.Contains(name, ".") {
		functionName = strings.TrimSuffix(name, "."+namespace)
	}

	nsEndpointLister := l.GetLister(namespace)

	if nsEndpointLister == nil {
		l.SetLister(namespace, l.EndpointLister.Endpoints(namespace))

		nsEndpointLister = l.GetLister(namespace)
	}

	svc, err := nsEndpointLister.Get(functionName)
	if err != nil {
		return url.URL{}, fmt.Errorf("error listing \"%s.%s\": %s", functionName, namespace, err.Error())
	}

	if len(svc.Subsets) == 0 {
		return url.URL{}, fmt.Errorf("no subsets available for \"%s.%s\"", functionName, namespace)
	}

	addresses := svc.Subsets[0].Addresses
	for _, addr := range addresses {
		if _, exist := l.RequestsTracing[addr.IP]; !exist {
			l.Lock.Lock()
			l.RequestsTracing[addr.IP] = 0
			l.EndpointsIps = append(l.EndpointsIps, addr.IP)
			l.Lock.Unlock()
		}
	}

	if len(svc.Subsets[0].Addresses) == 0 {
		return url.URL{}, fmt.Errorf("no addresses in subset for \"%s.%s\"", functionName, namespace)
	}

	serviceIP := l.GetTargetBasedOnLoadBalancingType()

	urlStr := fmt.Sprintf("http://%s:%d", serviceIP, watchdogPort)

	urlRes, err := url.Parse(urlStr)
	if err != nil {
		return url.URL{}, err
	}

	return *urlRes, nil
}

func (l *FunctionLookup) verifyNamespace(name string) error {
	if name != "kube-system" {
		return nil
	}
	// ToDo use global namepace parse and validation
	return fmt.Errorf("namespace not allowed")
}

func (l *FunctionLookup) GetTargetBasedOnLoadBalancingType() string {
	var target string
	switch l.LoadBalancingType {
	case "least_connections":
		//简单遍历寻找最小连接
		l.Lock.RLock()
		minIdx := 0
		for idx, ip := range l.EndpointsIps {
			if l.RequestsTracing[ip] < l.RequestsTracing[l.EndpointsIps[minIdx]] {
				minIdx = idx
			}
		}
		l.RequestsTracing[l.EndpointsIps[minIdx]] += 1
		target = l.EndpointsIps[minIdx]
		l.Lock.RUnlock()
	case "random":
		//default
		idx := rand.Intn(len(l.EndpointsIps))
		target = l.EndpointsIps[idx]
	case "round_robin":
		target = l.EndpointsIps[l.Next]
		l.Next = (l.Next + 1) % len(l.EndpointsIps)
	}
	return target
}
