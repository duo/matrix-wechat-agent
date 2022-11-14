# matrix-wechat-agent
An agent for [matrix-wechat](https://github.com/duo/matrix-wechat) based on [ComWeChatRobot](https://github.com/ljc545w/ComWeChatRobot).


### Building
```sh
GOOS=windows GOARCH=386 go build -o matrix-wechat-agent.exe main.go
```

### Dependencies
* SWeChatRobot.dll, wxDriver.dll, wxDriver64.dll (https://github.com/ljc545w/ComWeChatRobot)
* Visual C++ Redistributable (https://docs.microsoft.com/en-US/cpp/windows/latest-supported-vc-redist?view=msvc-170)

### Launch
```sh
matrix-wechat-agent.exe -h wss://example.com:port -s foobar
```

### Parameters
| Parameter | Function            |
| :-------: | ------------------- |
|   `-h`    | appservice address  |
|   `-s`    | secret              |
|   `-V`    | wechat fake version |
