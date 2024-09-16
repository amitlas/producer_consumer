package main

import (
    "prod_cons/common"
)

type Config struct {
    ZeroMQComm		string				`json:"zero_mq_protocol"`
    RateLimit		int					`json:"rate_limit"`
    ZMQHostName     string              `json:"zmq_host_name"`
    DBConnConfig		utils.DBConfig	`json:"db_conn_config"`
    Logging struct {
        Level	string			`json:"level"`
        Type		string			`json:"type"`
    } `json:"logging"`
    /*
    Monitoring struct {
        PrometheusPort		int `json:"prometheus_port"`
        PrometheusEndPoint	string `json:"prometheus_end_point"`
        ProfilingPort	string				`json:"profiling_port"`
    } `json:"monitorig"`
    */
}

