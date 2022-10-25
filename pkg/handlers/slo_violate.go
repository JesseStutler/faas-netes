package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/openfaas/faas-netes/pkg/common"
	types "github.com/openfaas/faas-provider/types"
	"io/ioutil"
	k8sv1core "k8s.io/api/core/v1"
	clientgov1core "k8s.io/client-go/informers/core/v1"
	"log"
	"strconv"
	"sync"
	"time"

	"net/http"
)

func MakeSLOViolateHandlers(promAddress string, promMetricType string, podInformer clientgov1core.PodInformer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := ioutil.ReadAll(r.Body)
		var alert types.AlertInfo
		err := json.Unmarshal(body, &alert)
		if err != nil {
			wrappedErr := fmt.Errorf("failed to unmarshal request: %s", err.Error())
			http.Error(w, wrappedErr.Error(), http.StatusBadRequest)
			return
		}
		nodeName := alert.CommonLabels["nodeName"]
		alertPodName := alert.CommonLabels["kubernetes_pod_name"]
		podsOnSameNode, err := podInformer.Informer().GetIndexer().ByIndex("nodeName", nodeName)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Unable to get pods cache by the nodeName indexer"))
			log.Fatalln(err)
			return
		}
		//配置Prometheus http客户端
		promClient := &http.Client{
			Timeout: 5 * time.Second,
		}
		var wg sync.WaitGroup
		faasHeap := &common.FaaSHeap{Metrics: &common.FaaSMetrics{}}
		for _, obj := range podsOnSameNode {
			pod := obj.(*k8sv1core.Pod)
			if pod.Name == alertPodName {
				continue
			}
			wg.Add(1)
			//开启多个goroutine对prometheus进行查询
			go func() {
				defer wg.Done()
				functionName := pod.Labels["faas_function"]
				promRequest := types.CreatePromQLRequestByType(promMetricType, functionName, nodeName)
				promReqBytes, _ := json.Marshal(promRequest)
				resp, err := promClient.Post(promAddress, "application/json", bytes.NewBuffer(promReqBytes))
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Failed to get results from prometheus"))
					log.Fatalln(err)
					return
				}
				respBody, _ := ioutil.ReadAll(resp.Body)
				var promResp types.PromQLResponse
				err = json.Unmarshal(respBody, &promResp)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Failed to unmarshal promQL results"))
					log.Fatalln(err)
					return
				}
				metricValue, _ := strconv.ParseFloat(promResp.Data.Result[0].Value[1].(string), 64)
				//并发安全
				faasHeap.Push(&common.FaaSMetric{
					FunctionName: functionName,
					PodName:      pod.Name,
					MetricValue:  metricValue,
				})
			}()
		}
		wg.Wait()

		//水平扩缩和流量管理逻辑

	}
}
