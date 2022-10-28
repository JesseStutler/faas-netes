package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/openfaas/faas-netes/pkg/common"
	types "github.com/openfaas/faas-provider/types"
	"io/ioutil"
	k8sv1core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgov1core "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"log"
	"strconv"
	"sync"
	"time"

	"net/http"
)

func MakeSLOViolateHandlers(promAddress string, promMetricType string, defaultNamespace string,
	podInformer clientgov1core.PodInformer, clientset *kubernetes.Clientset) http.HandlerFunc {
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
		alertFunctionName := alert.CommonLabels["faas_function"]
		upperLimitString := alert.CommonLabels["latency_upper_limit"]
		latencyUpperLimit, _ := strconv.ParseFloat(upperLimitString, 64)
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

		//水平扩容和流量管理逻辑
		maxMetric := faasHeap.Top()
		options := metav1.GetOptions{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Deployment",
				APIVersion: "apps/v1",
			},
		}
		deployment, err := clientset.AppsV1().Deployments(defaultNamespace).Get(context.TODO(),
			maxMetric.FunctionName, options)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Unable to lookup function deployment " + maxMetric.FunctionName))
			log.Fatalln(err)
			return
		}
		nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		oldReplicas := *deployment.Spec.Replicas
		//计算可供函数pod调度的节点数量
		nodesCanSchedule := 0
		for _, node := range nodes.Items {
			if _, master := node.Labels["node-role.kubernetes.io/master"]; master {
				continue
			}
			if _, openfaas := node.Labels["node-role.kubernetes.io/openfaas"]; openfaas {
				continue
			}
			nodesCanSchedule++
		}
		//pod现有数量小于可调度节点数才进行水平扩容
		if int(oldReplicas) < nodesCanSchedule {
			newReplicas := oldReplicas + 1
			deployment.Spec.Replicas = &newReplicas
			_, err = clientset.AppsV1().Deployments(defaultNamespace).Update(context.TODO(), deployment, metav1.UpdateOptions{})
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("Unable to update function deployment " + maxMetric.FunctionName))
				log.Fatalln(err)
				return
			}
			for !checkUntilDoNotAlert(promClient, promAddress, alertFunctionName, nodeName, latencyUpperLimit) {
				time.Sleep(5 * time.Second)
				//loop here, wait 5 seconds each time
			}
		} else {
			//如果节点上都充满了pod，那么控制流量
		}
		w.WriteHeader(http.StatusOK)
	}
}

func checkUntilDoNotAlert(promClient *http.Client, promAddress, alertFunctionName, alertNodeName string,
	latencyUpperLimit float64) bool {
	query := fmt.Sprintf("histogram_quantile(0.99, sum by (faas_function,kubernetes_pod_name,node_name,le) "+
		"(rate(http_request_duration_seconds_bucket{faas_function=\"%s\",node_name=\"%s\"}[30s])))", alertFunctionName,
		alertNodeName)
	promRequest := types.PromQLRequest{Query: query}
	promReqBytes, _ := json.Marshal(promRequest)
	resp, _ := promClient.Post(promAddress, "application/json", bytes.NewBuffer(promReqBytes))
	respBody, _ := ioutil.ReadAll(resp.Body)
	var promResp types.PromQLResponse
	json.Unmarshal(respBody, &promResp)
	metricValue, _ := strconv.ParseFloat(promResp.Data.Result[0].Value[1].(string), 64)
	if metricValue < latencyUpperLimit {
		return true
	}
	return false
}
