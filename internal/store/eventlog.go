// Package store 实现协调事件的只追加日志。
//
// 日志为每行一个事件信封的 JSONL 文件：写入只追加并 fsync，
// 进程重启时按顺序重放恢复状态。已写入的运输与展出记录因此不会
// 被后续改期命令覆盖——状态机只允许追加新事实。
//
// Path 为空时退化为纯内存日志，便于测试与本地试用。
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"example.com/batch-092001-q007/internal/domain"
)

// ErrEventExists 在 event_id 与历史事件冲突但载荷不同的场景由上层使用；
// 日志层本身把完全相同的重放视为成功（幂等）。
var ErrEventExists = errors.New("event already exists")

// EventLog 是带互斥的只追加事件日志。
type EventLog struct {
	mu   sync.Mutex
	path string
	file *os.File
	w    *bufio.Writer
	seen map[string]bool // 已落盘的 event_id
}

// Open 打开（或创建）JSONL 日志并把已有事件交给 handle 重放。
// handle 按落盘顺序逐行收到事件；返回错误会中止启动，避免
// 在损坏的状态上继续接受命令。
func Open(path string, handle func(domain.Event) error) (*EventLog, error) {
	l := &EventLog{path: path, seen: make(map[string]bool)}
	if path == "" {
		return l, nil
	}
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	l.file = f
	if err := l.replay(handle); err != nil {
		_ = f.Close()
		return nil, err
	}
	l.w = bufio.NewWriter(f)
	return l, nil
}

func (l *EventLog) replay(handle func(domain.Event) error) error {
	scanner := bufio.NewScanner(l.file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt domain.Event
		if err := json.Unmarshal(line, &evt); err != nil {
			return fmt.Errorf("event log line %d: %w", lineNo, err)
		}
		if l.seen[evt.EventID] {
			return fmt.Errorf("event log line %d: duplicate event_id %q", lineNo, evt.EventID)
		}
		l.seen[evt.EventID] = true
		if err := handle(evt); err != nil {
			return fmt.Errorf("event log line %d (%s): %w", lineNo, evt.EventID, err)
		}
	}
	return scanner.Err()
}

// Append 把事件序列化并原子追加。duplicate 返回 true 表示该 event_id
// 已存在——这是重放/重试路径，调用方按幂等处理，不再改变状态。
func (l *EventLog) Append(evt domain.Event) (duplicate bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen[evt.EventID] {
		return true, nil
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return false, fmt.Errorf("marshal event: %w", err)
	}
	if l.path == "" {
		l.seen[evt.EventID] = true
		return false, nil
	}
	if _, err := l.w.Write(data); err != nil {
		return false, fmt.Errorf("append event: %w", err)
	}
	if err := l.w.WriteByte('\n'); err != nil {
		return false, err
	}
	if err := l.w.Flush(); err != nil {
		return false, err
	}
	if err := l.file.Sync(); err != nil { // 崩溃前已确认的命令不丢
		return false, fmt.Errorf("sync event log: %w", err)
	}
	l.seen[evt.EventID] = true
	return false, nil
}

// Has 报告 event_id 是否已经落日志。
func (l *EventLog) Has(eventID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seen[eventID]
}

// Close 刷新并关闭日志文件。
func (l *EventLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	if err := l.w.Flush(); err != nil {
		return err
	}
	return l.file.Close()
}

// Snapshot 只读导出已提交事件，供离线核对；内存模式下日志不保存事件体，
// 因此返回空，核对应以服务内投影为准。
func (l *EventLog) Snapshot() ([]domain.Event, error) {
	l.mu.Lock()
	path := l.path
	l.mu.Unlock()
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []domain.Event
	dec := json.NewDecoder(f)
	for {
		var evt domain.Event
		if err := dec.Decode(&evt); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		out = append(out, evt)
	}
	return out, nil
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}
