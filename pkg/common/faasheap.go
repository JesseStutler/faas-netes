package common

import (
	"container/heap"
	"sync"
)

// FaasHeap 是用来进行指标排序的大顶堆，并发安全，支持多读者
type FaaSHeap struct {
	mutex   sync.RWMutex
	Metrics *FaaSMetrics
}

func (h *FaaSHeap) Push(metric *FaaSMetric) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	heap.Push(h.Metrics, metric)
}

func (h *FaaSHeap) Pop() *FaaSMetric {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	m := heap.Pop(h.Metrics).(*FaaSMetric)
	return m
}

func (h *FaaSHeap) Top() *FaaSMetric {
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	metrics := h.Metrics
	return (*metrics)[0]
}

// FixTheTop 修改堆顶的指标值，并重新调整堆
func (h *FaaSHeap) FixTheTop(value float64) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	top := h.Top()
	top.MetricValue = value
	heap.Fix(h.Metrics, 0)
}

type FaaSMetric struct {
	FunctionName string
	PodName      string
	MetricValue  float64
}

type FaaSMetrics []*FaaSMetric

func (f *FaaSMetrics) Push(x interface{}) {
	*f = append(*f, x.(*FaaSMetric))
}

func (f *FaaSMetrics) Pop() interface{} {
	v := (*f)[f.Len()-1]
	*f = (*f)[:f.Len()-1]
	return v
}

func (f FaaSMetrics) Less(i, j int) bool {
	return f[i].MetricValue > f[j].MetricValue
}

func (f FaaSMetrics) Swap(i, j int) {
	f[i], f[j] = f[j], f[i]
}

func (f FaaSMetrics) Len() int {
	return len(f)
}
