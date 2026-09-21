package main

import (
	"log"
	"net/http"
	"os"

	"example.com/batch-092001-q007/internal/api"
	"example.com/batch-092001-q007/internal/service"
)

func main() {
	// EVENT_LOG_PATH 为空时使用纯内存日志，便于本地试用；
	// 生产环境应指向持久卷上的 JSONL 文件，见 scripts/migrate.sh。
	logPath := os.Getenv("EVENT_LOG_PATH")
	coord, err := service.New(logPath, nil)
	if err != nil {
		log.Fatalf("初始化协调器失败: %v", err)
	}
	defer coord.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	})
	mux.Handle("/v1/", api.NewHandler(coord))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("展陈协调器监听 :%s（事件日志：%q）", port, logPath)
	if err := http.ListenAndServe("0.0.0.0:"+port, mux); err != nil {
		log.Fatal(err)
	}
}
