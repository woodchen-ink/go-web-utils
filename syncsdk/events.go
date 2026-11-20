package syncsdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// EventStream 是一条打开的 SSE 连接。Next 阻塞读取下一条事件; 连接断开返回错误,
// 由调用方 (Engine.Subscribe) 决定重连。用完调用 Close。
type EventStream struct {
	body io.ReadCloser
	r    *bufio.Reader
}

// Events 打开空间的 SSE 事件流 (GET /spaces/{space}/events)。
// lastSeq > 0 时以 Last-Event-ID 告知服务端已知序号, 服务端可立即补发一条 changed。
// 连接建立失败按 Client 的重试策略重试; 建立后的断线不重试。
func (c *Client) Events(ctx context.Context, space string, lastSeq int64) (*EventStream, error) {
	h := http.Header{"Accept": {"text/event-stream"}, "Cache-Control": {"no-cache"}}
	if lastSeq > 0 {
		h.Set("Last-Event-ID", strconv.FormatInt(lastSeq, 10))
	}
	resp, err := c.send(ctx, &request{method: http.MethodGet, path: []string{"spaces", space, "events"}, headers: h})
	if err != nil {
		return nil, err
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: events content type %q", ErrProtocol, ct)
	}
	return &EventStream{body: resp.Body, r: bufio.NewReaderSize(resp.Body, 16<<10)}, nil
}

// maxEventSize 限制单条事件体积, 防止异常服务端撑爆内存。
const maxEventSize = 1 << 20

// Next 读取下一条已知类型的事件; 注释行 (心跳) 与未知事件类型被跳过。
func (s *EventStream) Next() (Event, error) {
	var typ string
	var data strings.Builder
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			return Event{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if data.Len() > 0 || typ != "" {
				ev, ok, err := parseEvent(typ, data.String())
				if err != nil {
					return Event{}, err
				}
				if ok {
					return ev, nil
				}
			}
			typ = ""
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			typ = value
		case "data":
			if data.Len()+len(value) > maxEventSize {
				return Event{}, fmt.Errorf("%w: event too large", ErrProtocol)
			}
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
}

func parseEvent(typ, data string) (Event, bool, error) {
	if typ == "" {
		typ = "message"
	}
	switch typ {
	case EventChanged:
		var v struct {
			Seq int64 `json:"seq"`
		}
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			return Event{}, false, fmt.Errorf("%w: changed event", ErrProtocol)
		}
		return Event{Type: typ, Seq: v.Seq}, true, nil
	case EventPresence:
		var v struct {
			Presence []Presence `json:"presence"`
		}
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			return Event{}, false, fmt.Errorf("%w: presence event", ErrProtocol)
		}
		return Event{Type: typ, Presence: v.Presence}, true, nil
	}
	return Event{}, false, nil
}

// Close 关闭连接。
func (s *EventStream) Close() error { return s.body.Close() }
