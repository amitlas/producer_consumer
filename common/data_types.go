package utils

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
