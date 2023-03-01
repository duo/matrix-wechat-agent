# matrix-wechat-agent
An agent for [matrix-wechat](https://github.com/duo/matrix-wechat) based on [ComWeChatRobot](https://github.com/ljc545w/ComWeChatRobot).


### Building
```sh
GOOS=windows GOARCH=386 go build -o matrix-wechat-agent.exe main.go
```

### Dependencies
* SWeChatRobot.dll, wxDriver.dll, wxDriver64.dll (https://github.com/ljc545w/ComWeChatRobot)
* Visual C++ Redistributable (https://docs.microsoft.com/en-US/cpp/windows/latest-supported-vc-redist?view=msvc-170)
* WeChat 3.7.0.30 (Unofficial: https://github.com/tom-snow/wechat-windows-versions/releases/tag/v3.7.0.30)

## Configuration
* configure.yaml
```yaml
limb:
  version: 3.8.1.26 # Required, disguised WeChat version
  listen_port: 22222 # Required, port for listening WeChat message
  init_timeout: 10s # Optional, WeChat client initialization timeout
  request_timeout: 30s # Optional

service:
  addr: ws://10.10.10.10:11111 # Required, ocotpus address
  secret: hello # Reuqired, user defined secret
  ping_interval: 30s # Optional

log:
  level: info
```
