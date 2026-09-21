# 领域约定

国际影像展陈协调器围绕影像作品、场地和展示权利保存可核对的业务记录。外部主体使用不含真实身份信息的稳定引用编号，时间采用带偏移量的 ISO 8601 字符串，材料只保存受控引用和 `sha256` 摘要。

交换事件包含 `event_id`、`subject_ref`、`occurred_at`、`source_sequence` 和 `payload_digest`。来源序号只在同一来源内递增，接收方必须保留原始发生时间，不得用到达时间覆盖。示例内容仅用于说明字段形状，不代表真实人员、机构或业务结论。

## 稳定编号

| 前缀 | 含义 |
| --- | --- |
| `ART-` | 作品（逻辑作品，可含多个组成件） |
| `PIECE-` | 可独立运输、占用与安装的实体组成件 |
| `RGR-` | 权利人授权声明 |
| `LOAN-` | 借展协议 |
| `VEN-` | 展厅、街巷、院落 |
| `DEV-` | 可独占占用的播放/展示设备 |
| `SLOT-` | 作品在场地某时段的一次展陈安排（场次） |
| `DEP-` | 安装前置依赖 |
| `MV-` | 运输/展出过程记录 |
| `DSP-` | 处置方案 |

## 核心不变量

1. **实体占用互斥**：同一作品在两个未取消场次的时段（半开区间 `[start,end)`，首尾相接不冲突）不得重叠；同一设备同理。
2. **公开展示三要素**：场次覆盖的**每一个展馆当地日期**、每一种媒介，都必须存在未撤回的授权声明，同时满足：国家在 `allowed_countries` 内（空为不限）、媒介在 `allowed_media` 内（空为不限）、当地日期落在 `[valid_from, valid_to]`（闭区间）。
3. **跨时区落位**：权利日期按场地 `tz`（IANA 名，如 `Asia/Shanghai`）把绝对时刻换算为当地日历日期后比较，不按提交方或 UTC 日期判定。场次时刻本身保留为带偏移量的绝对时刻。
4. **借展截止**：场次结束不得晚于任一借展协议的 `must_return_by`。
5. **安装前置项**：确认场次时，其全部依赖（前置场次达到 `delivered`/`installed` 阶段，阶段由运输/展出记录判定）必须已满足；计划时不拦截。
6. **设备匹配**：组成件声明的 `required_device_kinds` 必须由场次设备覆盖；设备必须属于该场地且未故障。
7. **历史不可覆盖**：运输/展出记录只追加；已有此类记录或开始时刻已过的场次不得改期；取消场次不删除历史事实。
8. **审计**：作品损坏、权利撤回、场地关闭、设备故障均生成处置方案，列出受影响场次与具体动作（暂停定损、撤出展示、迁移、更换设备），无可行替代时保持 `open_risk=true`，解决时追加解决事件与说明。

## 幂等与顺序

- 同一 `event_id` 的重放（含进程重启后）返回首次结果，不产生新事件、不重复副作用；重放短路先于业务校验，因此即使投影已变化（如撤权后重传旧排期）也不会报出新冲突。
- `source_id` + `source_sequence` 在同一来源内必须连续递增（允许末位序号的重放）；序号跳跃、回退或被其他事件占用都返回冲突。
- 院校批量重传清单时，组成件以 `manifest:<manifest_ref>:piece:<ref>` 为自然幂等键；携带新 `event_id` 的重传同样幂等，并返回首次事件编号。

## 事件类型

登记：`artwork_registered`、`piece_registered`、`rights_recorded`、`loan_recorded`、`venue_registered`、`device_registered`、`install_dependency_added`。

排期：`slot_planned`、`slot_confirmed`、`slot_cancelled`、`slot_rescheduled`。

事实与风险：`movement_appended`、`damage_reported`、`rights_withdrawn`、`venue_closed`、`device_broken`、`disposition_generated`、`disposition_resolved`。

所有事件逐行写入 JSONL 只追加日志并 `fsync`；进程启动按落盘顺序重放重建投影。日志损坏会中止启动，避免在不一致状态上接受命令。
