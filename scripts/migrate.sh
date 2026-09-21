#!/bin/sh
set -eu
mkdir -p "${DATABASE_PATH:-./data}"
printf '%s
' '当前实现使用内存事实库与只追加事件台账，进程启动即为空库；持久化版本将在此目录初始化。'
