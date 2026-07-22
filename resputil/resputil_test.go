package resputil

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// decode 解析响应体为通用 map, 便于断言字段
func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, body=%s", err, body)
	}
	return m
}

func TestOK(t *testing.T) {
	rec := httptest.NewRecorder()
	OK(rec, map[string]string{"name": "wood"})

	if rec.Code != 200 {
		t.Errorf("HTTP status = %d, expected 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	m := decode(t, rec.Body.Bytes())
	if m["code"].(float64) != 200 || m["msg"] != "success" {
		t.Errorf("响应字段不符: %v", m)
	}
	if m["data"].(map[string]any)["name"] != "wood" {
		t.Errorf("data 不符: %v", m["data"])
	}
}

func TestFail(t *testing.T) {
	rec := httptest.NewRecorder()
	Fail(rec, 40001, "参数错误")

	if rec.Code != 200 {
		t.Errorf("Fail 应保持 HTTP 200, got %d", rec.Code)
	}
	m := decode(t, rec.Body.Bytes())
	if m["code"].(float64) != 40001 || m["msg"] != "参数错误" {
		t.Errorf("响应字段不符: %v", m)
	}
	// data 必须是空对象而非 null
	if data, ok := m["data"].(map[string]any); !ok || len(data) != 0 {
		t.Errorf("data 应为空对象, got %v", m["data"])
	}
}

func TestFailStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	FailStatus(rec, 401, 40100, "未登录")

	if rec.Code != 401 {
		t.Errorf("HTTP status = %d, expected 401", rec.Code)
	}
	m := decode(t, rec.Body.Bytes())
	if m["code"].(float64) != 40100 {
		t.Errorf("code 不符: %v", m)
	}
}

func TestNormalizeNullValues(t *testing.T) {
	tests := []struct {
		name     string
		data     any
		expected string // data 字段序列化后的形态
	}{
		{name: "nil归一为空对象", data: nil, expected: "{}"},
		{name: "nil slice归一为空数组", data: []string(nil), expected: "[]"},
		{name: "nil map归一为空对象", data: map[string]int(nil), expected: "{}"},
		{name: "nil指针归一为空对象", data: (*struct{ X int })(nil), expected: "{}"},
		{name: "非空slice原样输出", data: []int{1, 2}, expected: "[1,2]"},
		{name: "空字符串原样输出", data: "", expected: `""`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			OK(rec, tt.data)

			var m map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if string(m["data"]) != tt.expected {
				t.Errorf("data = %s, expected %s", m["data"], tt.expected)
			}
		})
	}
}

func TestMarshalFailureFallback(t *testing.T) {
	rec := httptest.NewRecorder()
	OK(rec, make(chan int)) // channel 无法序列化

	if rec.Code != 500 {
		t.Errorf("序列化失败应降级 500, got %d", rec.Code)
	}
}
