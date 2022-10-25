package types

import "fmt"

type PromQLRequest struct {
	Query string `json:"query"`          //查询指标名
	Time  string `json:"time,omitempty"` //查询时间戳
}

type PromQLResponse struct {
	Status    string     `json:"status"`
	Data      PromQLData `json:"data"`
	ErrorType string     `json:"errorType,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type PromQLData struct {
	ResultType string         `json:"resultType"`
	Result     []PromQLResult `json:"result"`
}

type PromQLResult struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`
}

func CreatePromQLRequestByType(promMetricType string, functionName string, nodeName string) *PromQLRequest {
	req := &PromQLRequest{}
	switch promMetricType {
	case "QPS":
		req.Query = fmt.Sprintf("rate(http_requests_total{faas_function=\"%s\",node_name=\"%s\"}[30m])", functionName, nodeName)
	case "CPU":
	}
	return req
}
