package main
import (
    "prod_cons/common"
)

type Config struct {
    ZeroMQComm          string                  `json:"zero_mq_protocol"`
    MaxBacklog          int                     `json:"max_backlog"`
    MsgProdRate         int                     `json:"msg_prod_rate"`
    DBConnConfig        utils.DBConfig          `json:"db_conn_config"`
    MonitoringConfig    utils.MontioringConfig  `json:"monitoring"`
    Logging struct {
        Level           string                  `json:"level"`
        Type            string                  `json:"type"`
        Output          string                  `json:"output"`
    } `json:"logging"`
}
