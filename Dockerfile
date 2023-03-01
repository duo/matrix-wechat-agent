FROM golang:1.20-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY ./ .

RUN set -ex && \
	cd /build && \
	GOOS=windows GOARCH=386 go build -o matrix-wechat-agent.exe main.go && \
	wget -q "https://github.com/ljc545w/ComWeChatRobot/releases/download/3.7.0.30-0.1.1-pre/3.7.0.30-0.1.1-pre.zip" -O CowWeChatRobot.zip && \
	unzip -q CowWeChatRobot.zip && \
	git clone https://github.com/tom-snow/docker-ComWechat.git dc && \
	wget -q "https://github.com/tom-snow/docker-ComWechat/releases/download/v0.2_wc3.7.0.30/Tencent.zip" -O dc/wine/Tencent.zip && \
	echo 'build done'

FROM zixia/wechat:3.3.0.115

ENV DISPLAY=:5 \
	VNCPASS=YourSafeVNCPassword

USER user
WORKDIR /home/user

EXPOSE 5905

RUN set ex && \
	sudo apt-get update && \
	sudo apt-get --no-install-recommends install dumb-init tigervnc-standalone-server tigervnc-common openbox wget -y

COPY --from=builder /build/dc/wine/simsun.ttc  /home/user/.wine/drive_c/windows/Fonts/simsun.ttc
COPY --from=builder /build/dc/wine/微信.lnk /home/user/.wine/drive_c/users/Public/Desktop/微信.lnk
COPY --from=builder /build/dc/wine/system.reg  /home/user/.wine/system.reg
COPY --from=builder /build/dc/wine/user.reg  /home/user/.wine/user.reg
COPY --from=builder /build/dc/wine/userdef.reg /home/user/.wine/userdef.reg

COPY --from=builder /build/dc/wine/Tencent.zip /Tencent.zip
COPY --from=builder /build/scripts/run.py /usr/bin/run.py
COPY --from=builder /build/matrix-wechat-agent.exe /home/user/matrix-wechat-agent/matrix-wechat-agent.exe
COPY --from=builder /build/http/SWeChatRobot.dll /home/user/matrix-wechat-agent/SWeChatRobot.dll
COPY --from=builder /build/http/wxDriver.dll /home/user/matrix-wechat-agent/wxDriver.dll

RUN set -ex && \
	sudo chmod a+x /usr/bin/run.py && \
	rm -rf "/home/user/.wine/drive_c/Program Files/Tencent/" && \
	unzip -q /Tencent.zip && \
	cp -rf wine/Tencent "/home/user/.wine/drive_c/Program Files/" && \
	sudo rm -rf wine Tencent.zip && \
	sudo apt-get autoremove -y && \
	sudo apt-get clean && \
	sudo rm -fr /tmp/* && \
	echo 'build done'

WORKDIR /home/user/matrix-wechat-agent

ENTRYPOINT ["/usr/bin/dumb-init"]
CMD ["/usr/bin/run.py"]
