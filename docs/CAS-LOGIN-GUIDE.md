# XJTU CAS 登录对接指南

基于西安交通大学 CAS (Apereo CAS 6.x) 的完整登录流程，含验证码和 MFA 处理。

## 关键配置（从 CAS 登录页提取）

```
mfaFirstNeed: true          # MFA 从首次登录就需要
captchaSkipN: 3             # 前 3 次不显示验证码
mfaEnabled: true            # MFA 开关
secEnabled: true            # 安全验证 (Safety Verify) 开关
mfaStrategyDynamic: true    # 动态 MFA 策略
encryptEnabled: true        # 密码加密
```

## 1. CAS 会话管理

**核心原则：整个登录流程使用同一个 HTTP 客户端（Cookie Jar）。**

```
NewClient() → 创建带 Cookie Jar 的客户端
     ↓
GET xkfw → CAS 重定向 → 设置 CAS Session Cookie
     ↓
所有后续请求复用同一客户端（Cookie 保持一致）
```

### Go 实现要点

- 使用 `cookiejar.New(nil)` 创建 Cookie Jar
- 所有请求（GET/POST）共享同一个 `*http.Client`
- 验证码重试时**不能**重新做 GET 请求（会刷新 CAS Session，使 execution 失效）

```go
// 正确：复用已保存的客户端
if isRetry {
    client = savedClient  // 保持 Cookie Jar 不变
}
```

## 2. 密码加密

```
GET https://login.xjtu.edu.cn/cas/jwt/publicKey
     ↓
RSA PKCS1_v1_5 加密密码
     ↓
Base64 编码 → 前面加 "__RSA__" 前缀
```

- 公钥缓存：整个会话期间公钥不变，只需获取一次
- 重登录时：公钥通常是新的，但缓存的也能用

## 3. 设备指纹 (fpVisitorId)

生成 32 位十六进制指纹，用于 CAS 识别设备：

```go
fpID = SHA256(platform + hostname + numCPU + mac)[:32]
```

- 整个会话使用同一个 fpID
- 所有 POST（登录、MFA detect、账户选择、Safety Verify）都要带 `fpVisitorId`

## 4. MFA 检测

```
POST https://login.xjtu.edu.cn/cas/mfa/detect
Content-Type: application/x-www-form-urlencoded

username={account}&password={encPwd}&fpVisitorId={fpID}&loginType=passwordLogin
```

响应：
```json
{"code": 0, "data": {"need": true, "state": "XXXX"}}
```

- `need: true` → 需要 MFA，保存 `state` 用于后续流程
- `need: false` → 不需要 MFA，但该校 `mfaFirstNeed=true` 所以总是 true
- MFA 检测在每次提交登录表单前调用

## 5. 登录表单提交

```
POST https://login.xjtu.edu.cn/cas/login?service=...
Content-Type: application/x-www-form-urlencoded

username={account}
password={encPwd}
execution={execution}
_eventId=submit
submit1=Login1
fpVisitorId={fpID}
captcha={captcha}
currentMenu=1
failN={failCount}
mfaState={mfaState}
geolocation=
trustAgent={true|false|""}
```

### execution 提取

从 CAS 登录页 HTML 中提取：
```go
// <input type="hidden" name="execution" value="..." />
func extractExecution(html []byte) string {
    // 搜索 name="execution" → value="..."
}
```

### 响应处理（按优先级）

1. **HTTP 401** → 用户名或密码错误
2. **`<el-alert>` 元素** → 提取 `title` 属性，返回错误消息
3. **Safety Verify 页面**（含 `secState`）→ 需要二次安全认证
4. **账户选择页面**（含 `account-wrap`）→ 本/研身份选择
5. **CAS 登录页 + 验证码可见** → 需要验证码
6. **非 CAS 页面**（已重定向到 xkfw）→ 登录成功

## 6. 验证码流程

### 触发条件
- `failCount >= 3` 时 CAS 显示验证码（`captchaSkipN: 3`）
- CAS 登录页 HTML 中 `captcha.jpg` 始终存在，但未触发时被 `display:none` 隐藏

### 正确做法（关键！）
**failCount >= 3 时，不要向 CAS 发 POST，直接返回"需要验证码"。**
这是 XJTUToolBox 的做法，避免触发 CAS 的 reCAPTCHA 验证。

```go
if failCount >= 3 && captcha == "" {
    // 不 POST，直接返回
    return &CaptchaNeededError{}
}
```

### 验证码图片
```
GET https://login.xjtu.edu.cn/cas/captcha.jpg
```
- 使用**同一个** HTTP 客户端（Cookie Jar 一致）
- 图片关联到当前 CAS Session

### 验证码重试
- 复用之前保存的 CAS Session（不刷新 Cookie）
- **重新检测 MFA**（execution 变了，mfaState 也需要更新）
- CAS 返回的验证码页面会包含新 execution，需要提取并存储

## 7. MFA 验证流程

### 7.1 初始化
```
GET https://login.xjtu.edu.cn/cas/{flow}/initByType/{type}?state={mfaState}
  flow: "mfa" 或 "sec"（Safety Verify）
  type: "securephone" 或 "secureemail"
```

响应：
```json
{"code": 0, "data": {
    "gid": "...",
    "attestServerUrl": "https://login.xjtu.edu.cn/attest",
    "securePhone": "131****6682"
}}
```

### 7.2 发送验证码
```
POST https://login.xjtu.edu.cn/attest/api/guard/{type}/send
Content-Type: application/json        ← 注意：是 JSON，不是 form-encoded！

{"gid": "..."}
```

### 7.3 校验验证码
```
POST https://login.xjtu.edu.cn/attest/api/guard/{type}/valid
Content-Type: application/json

{"gid": "...", "code": "123456"}
```

响应：
```json
{"code": 0, "data": {"result": "ok", "status": 2}}     // status 可能是 int 2 或 string "2"
```

**status 类型坑：必须同时处理 `int 2` 和 `string "2"`，否则正确验证码也会报错！**

### 7.4 完成登录（关键！）
**MFA 验证成功后，必须向 CAS 提交登录表单。不能重新调用 MFA 检测（因为 `mfaFirstNeed=true` 会死循环）。**

```go
// 正确：直接 POST 登录表单到 CAS
func CompleteMFALogin(client *resty.Client) error {
    // 1. GET xkfw → CAS 重定向 → 获取新 execution
    httpClient.Get(baseURL)
    casURL := responseURL
    execution := extractExecution(body)

    // 2. POST 登录表单（带 mfaState）
    postCASRaw(httpClient, casURL, account, encPwd, execution, mfaState, fpID, "", "")

    // 3. CAS 检查 MFA 已完成 → 重定向到 xkfw → 注册
}
```

## 8. Safety Verify 流程

当 CAS 返回 Safety Verify 页面（含 `secState`）时：

1. 提取 `secState`、`execution`、`_eventId` 等隐藏字段
2. 前端引导用户完成 MFA 验证（同 §7）
3. 提交 Safety Verify 表单：
   ```
   POST {safetyVerifyURL}
   secState={secState}
   execution={execution}
   _eventId=submit
   submit=Login1
   fpVisitorId={fpID}
   geolocation=
   ```
4. 后续走正常的重定向 + 注册流程

## 9. 注册（获取 Token）

登录成功后，CAS 重定向到 xkfw，调用 register.do：

```
GET https://xkfw.xjtu.edu.cn/xsxkapp/sys/xsxkapp/student/register.do?number=null
```

响应中包含 `token`，后续 API 请求需在 Header 中带上 `Token: {token}`。

## 10. 常见坑总结

| 问题 | 原因 | 修复 |
|------|------|------|
| 验证码重试永远失败 | 重试前做了 Head/GET 刷新了 CAS Session | 重试用保存的 client，不发起新 GET |
| 输正确验证码还报错 | POST 后先检查验证码页面再检查错误提示 | **先检查 `<el-alert>` 再检查验证码页面** |
| reCAPTCHA 错误反复出现 | failCount>=3 时仍向 CAS 发 POST | 预检 failCount，直接返回需要验证码 |
| 手机/邮箱收不到验证码 | `SendMFACode` 发 form-encoded | **改为 JSON**（attest 服务器要 `application/json`） |
| 输正确验证码提示错误 | `status` 字段类型判断 | 同时处理 `int 2` 和 `string "2"` |
| MFA 验证成功后 500 错误 | 重新调用 detectMFA 导致死循环 | 直接 POST 登录表单到 CAS |
| MFA 验证成功后登录失败 | 创建新 client 丢失 MFA cookies | 复用同一个 `s.client` |
