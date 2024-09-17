package utils

type MontioringConfig struct {
    PrometheusPort      int     `json:"prometheus_port"`
    PrometheusEndPoint  string  `json:"prometheus_end_point"`
    ProfilingPort       string  `json:"profiling_port"`
}

type DBConfig struct {
    Host     string `json:"host"`
    Port     int    `json:"port"`
    User     string `json:"user"`
    Password string `json:"password"`
    DBName   string `json:"dbname"`
}


type TaskMsg struct {
    ID  int     `json:"id"`
}
