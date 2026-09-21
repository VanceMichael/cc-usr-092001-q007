# 国际影像展陈协调器

平遥摄影大展的后端协调系统：保存数千名摄影师的作品及组成件、权利人声明、借展协议、场地条件、设备资源与安装依赖；排期确认时防止同一实体作品或设备在重叠时段被重复占用，并尊重作品只在特定国家、媒介与日期公开的限制。

作品损坏、权利撤回、场地关闭、设备故障会生成可审计的处置方案；运输与展出记录只追加、不可被重排覆盖；跨时区提交按展馆当地时间落位；院校批量重传清单保持幂等。工作人员可从作品编号查看完整展陈轨迹，也可从某场地某时段列出安装前置项、未解决风险与可替代方案。

## 架构

```
cmd/server           HTTP 入口（GET /health + /v1 JSON API）
internal/api         请求解码、错误码映射（400/404/409）
internal/service     命令校验、处置方案生成、查询投影（先校验→追加→归约）
internal/domain      领域模型、当地日期、半开区间重叠、授权判定
internal/store       JSONL 只追加事件日志（fsync、启动重放、event_id 去重）
internal/clock       可注入时钟（测试用固定时钟）
```

所有写操作都作为事件追加到日志，再由无业务判断的归约函数更新内存投影；进程重启按日志顺序重放恢复。约定见 [`docs/domain.md`](docs/domain.md)。

## 运行

```sh
make test      # 运行全部测试
make migrate   # 创建 data/events.jsonl（EVENT_LOG_PATH 可覆盖）
make run       # 启动服务，默认 :8080；不设 EVENT_LOG_PATH 时为纯内存模式
```

环境变量：`PORT`（默认 8080）、`EVENT_LOG_PATH`（如 `data/events.jsonl`）。

## API 概览

写接口均接受 JSON 命令并要求 `event_id`；可附带 `source_id` + `source_sequence` 做来源有序提交。时间字段为带偏移量的 RFC 3339。违反不变量返回 `409`，响应体为 `{"error":{"code":"conflict","reasons":[...]}}`。

| 方法与路径 | 作用 |
| --- | --- |
| `POST /v1/artworks` | 登记作品 |
| `POST /v1/pieces` | 登记组成件（支持 `manifest_ref` 批量幂等） |
| `POST /v1/rights` | 记录权利人声明（国家/媒介/日期窗口） |
| `POST /v1/loans` | 记录借展协议（撤展截止） |
| `POST /v1/venues` | 登记场地（国家代码 + IANA 时区） |
| `POST /v1/devices` | 登记播放/展示设备 |
| `POST /v1/dependencies` | 登记安装前置依赖 |
| `POST /v1/slots` | 计划场次（占用、权利、借展、设备即刻校验） |
| `POST /v1/slots/{ref}/confirm` | 确认场次（追加安装前置项校验） |
| `POST /v1/slots/{ref}/reschedule` | 改期未发生、无运输事实的场次 |
| `POST /v1/slots/{ref}/cancel` | 取消场次（历史事实保留） |
| `POST /v1/movements` | 追加运输/展出事实（shipped/delivered/installed/opened/closed/returned） |
| `POST /v1/incidents/damage` | 上报损坏并生成处置方案 |
| `POST /v1/incidents/rights-withdrawal` | 撤回权利并生成撤出/迁移方案 |
| `POST /v1/incidents/venue-closure` | 关闭场地并生成迁移方案 |
| `POST /v1/incidents/device-breakage` | 设备故障并生成换设备/迁移方案 |
| `POST /v1/dispositions/{ref}/resolve` | 解决处置方案（留审计说明） |
| `GET /v1/artworks/{ref}` | 作品与组成件 |
| `GET /v1/artworks/{ref}/track` | 完整展陈轨迹（场次/事实/处置，按时间合并） |
| `GET /v1/venues/{ref}/brief?start=&end=` | 场地时段简报：场次、前置项、未解决风险、可替代方案 |

## 快速示例

```sh
curl -s localhost:8080/v1/venues -d '{
  "event_id":"EVT-V1","ref":"VEN-PY","country_code":"CN","tz":"Asia/Shanghai"}'

curl -s localhost:8080/v1/slots -d '{
  "event_id":"EVT-S1","ref":"SLOT-1","artwork_ref":"ART-1","venue_ref":"VEN-PY",
  "start":"2026-09-25T10:00:00+08:00","end":"2026-09-25T12:00:00+08:00",
  "devices":["DEV-P1"]}'

curl -s "localhost:8080/v1/venues/VEN-PY/brief?start=2026-09-25T00:00:00%2B08:00&end=2026-09-26T00:00:00%2B08:00"
```

跨时区权利示例：提交时刻 `2026-09-25T16:30:00Z` 在上海已是 9 月 26 日，若授权只到 9 月 25 日，平遥场次被拒绝；同一瞬间柏林仍在 9 月 25 日，柏林场次可以落位。

## 配置与数据

配置通过环境变量传入；事件日志、敏感值和本地数据目录（`data/`）不提交到仓库。
