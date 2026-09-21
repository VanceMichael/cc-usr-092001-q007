package main

import (
	"log"
	"net/http"
	"os"
	_ "time/tzdata" // 容器内也能解析 Asia/Shanghai 等展馆 IANA 时区

	"example.com/batch-092001-q007/internal/api"
	"example.com/batch-092001-q007/internal/service"
	"example.com/batch-092001-q007/internal/store"
)

func main() {
	st := store.New()
	svc := service.New(st)
	server := api.New(svc)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("国际影像展陈协调器监听 :%s", port)
	if err := http.ListenAndServe("0.0.0.0:"+port, server.Handler()); err != nil {
		log.Fatal(err)
	}
}
