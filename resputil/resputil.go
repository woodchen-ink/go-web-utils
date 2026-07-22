/*
Package resputil 提供统一的 JSON 接口响应封装。

响应结构固定为 { "code": int, "data": any, "msg": string }:
  - code: 业务状态码, 200 表示成功
  - data: 业务数据; 空数据返回空对象 {} 或空数组 [], 永不返回 null
  - msg:  提示信息

使用示例:

	func handler(w http.ResponseWriter, r *http.Request) {
		user, err := loadUser(r)
		if err != nil {
			resputil.Fail(w, 40401, "用户不存在")
			return
		}
		resputil.OK(w, user)
	}
*/
package resputil

import (
	"encoding/json"
	"net/http"
	"reflect"
)

// CodeOK 成功业务码
const CodeOK = 200

// Response 统一 JSON 响应结构
type Response struct {
	Code int    `json:"code"`
	Data any    `json:"data"`
	Msg  string `json:"msg"`
}

// OK 输出成功响应 (HTTP 200, code 200, msg "success")
func OK(w http.ResponseWriter, data any) {
	Write(w, http.StatusOK, Response{Code: CodeOK, Data: data, Msg: "success"})
}

// OKMsg 输出带自定义提示的成功响应
func OKMsg(w http.ResponseWriter, data any, msg string) {
	Write(w, http.StatusOK, Response{Code: CodeOK, Data: data, Msg: msg})
}

// Fail 输出业务失败响应 (HTTP 200, data 为空对象);
// code 为业务错误码, 与 HTTP 状态解耦
func Fail(w http.ResponseWriter, code int, msg string) {
	Write(w, http.StatusOK, Response{Code: code, Data: nil, Msg: msg})
}

// FailStatus 输出带自定义 HTTP 状态码的失败响应 (data 为空对象),
// 用于 401/403/429 等需要网关或前端拦截器按 HTTP 状态处理的场景
func FailStatus(w http.ResponseWriter, httpStatus, code int, msg string) {
	Write(w, httpStatus, Response{Code: code, Data: nil, Msg: msg})
}

// Write 按统一结构输出 JSON 响应, 自动把空 data 归一化为空对象/空数组;
// 序列化失败时降级输出 HTTP 500 纯文本; 非法或 informational 状态码
// 矫正为 500, 避免 WriteHeader panic 或 1xx 语义破坏 JSON 响应体
func Write(w http.ResponseWriter, httpStatus int, resp Response) {
	if httpStatus < 200 || httpStatus > 999 {
		httpStatus = http.StatusInternalServerError
	}
	resp.Data = normalizeData(resp.Data)

	body, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "internal error: response marshal failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	_, _ = w.Write(body)
}

// emptyObject 空对象占位, 用于替换会序列化成 null 的空值
var emptyObject = map[string]any{}

// normalizeData 把会序列化为 null 的空值替换为空对象/空数组:
// nil、nil 指针、nil map 替换为 {}; nil slice 替换为 [];
// 非 nil 指针解引用后递归归一化 (覆盖 `&list` 且 list 为 nil slice 的
// 常见 GORM 查询写法)
func normalizeData(data any) any {
	if data == nil {
		return emptyObject
	}
	v := reflect.ValueOf(data)
	switch v.Kind() {
	case reflect.Slice:
		if v.IsNil() {
			return []any{}
		}
	case reflect.Map:
		if v.IsNil() {
			return emptyObject
		}
	case reflect.Ptr:
		if v.IsNil() {
			return emptyObject
		}
		return normalizeData(v.Elem().Interface())
	}
	return data
}
