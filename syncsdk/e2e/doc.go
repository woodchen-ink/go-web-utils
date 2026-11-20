/*
Package e2e 实现 CZL Connect 数据同步的端到端加密原语。本包不做任何 I/O:
密钥与密文以值的形式进出, 发往何处由上层 syncsdk 的同步引擎决定。

只使用标准构件: X25519 (crypto/ecdh)、AES-256-GCM、HMAC-SHA256、SHA-256、
Argon2id (golang.org/x/crypto/argon2) 与匿名 sealed box
(golang.org/x/crypto/nacl/box, X25519 + XSalsa20-Poly1305)。每个哈希、AAD 与封装明文
都以独立的域字符串开头, 拼接一律用大端 uint32 长度前缀 (LP), 不同输入不会编码相同。
二进制值在线上一律是无填充 base64url。

密钥:

  - UserKey: 用户长期 X25519 密钥对。公钥与指纹 (Fingerprint) 公开, 私钥存系统密钥库
  - RecoveryCode: 144 位随机数 + 16 位校验, 展示为 "XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX";
    经 Argon2id 派生恢复密钥, 加密出服务端保存的私钥副本 Escrow
  - Keyring: 一个空间的带版本 AES-256-GCM 密钥与不轮换的附件标识 HMAC 密钥,
    按成员用 sealed box 封装

内容:

  - Envelope: {"v":1,"kv":N,"nonce":..,"ct":..}, AAD 绑定空间、集合与文档 id
  - 附件流: 分块 AES-256-GCM, AAD 含块序号与结束标记; 附件标识 = HMAC-SHA256(附件标识密钥, 内容)
  - 新设备授权: 承诺 + 双方 nonce + 6 位 SAS, 之后把用户私钥封装给新设备临时公钥

本包拥有的秘密字节切片在用完后清零; crypto/ecdh 与 cipher.AEAD 内部的密钥无法清除, 交给 GC。

Documentation: https://go-web-utils.czl.net/docs/syncsdk
*/
package e2e
