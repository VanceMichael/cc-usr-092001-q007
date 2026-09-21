#!/bin/sh
# 初始化本地数据目录与只追加事件日志。
# EVENT_LOG_PATH 默认 data/events.jsonl；生产环境通过环境变量指向持久卷。
set -eu
EVENT_LOG_PATH="${EVENT_LOG_PATH:-data/events.jsonl}"
mkdir -p "$(dirname "$EVENT_LOG_PATH")"
if [ ! -f "$EVENT_LOG_PATH" ]; then
  : > "$EVENT_LOG_PATH"
  printf '%s\n' "已创建空事件日志：$EVENT_LOG_PATH"
else
  printf '%s\n' "事件日志已存在：$EVENT_LOG_PATH"
fi
