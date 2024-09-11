# Inspiration from:
#	https://github.com/li-xunhuan/wxhelper-docker
# 	https://github.com/wuxs/wxbot-docker/blob/master/Dockerfile
#	https://github.com/thinker007/wxhelper-docker
#	https://github.com/duo/matrix-wechat-docker
#	https://github.com/tom-snow/docker-ComWechat

FROM golang:1.20-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY ./internal ./internal
COPY ./main.go .
COPY ./go.mod .
COPY ./go.sum .

RUN set -ex && \
	cd /build && \
	GOOS=windows GOARCH=386 go build -v -o matrix-wechat-agent.exe main.go

FROM zixia/wine:6.0

ARG WECHAT_VERSION=3.9.10.19
ARG WXHELPER_VERSION=1

WORKDIR /home/user/.wine64/drive_c

USER root

# Port for the HTTP server embedded in the WeChat app, expose for testing
# EXPOSE 19088

RUN set ex && \
	apt-get update && \
	apt-get --no-install-recommends install -y \
		dumb-init \
		xdotool \
		x11-apps \
		# Install net-tools for netstat (debugging)
		net-tools \
		# Install iproute2 for ss (debugging)
		iproute2 \
		# Install winbind for ntlm_auth (wechat)
		winbind \
		# Install libldap for LDAP support (wechat)
		libldap-2.5-0

# Download WeChat
ADD https://github.com/tom-snow/wechat-windows-versions/releases/download/v${WECHAT_VERSION}/WeChatSetup-${WECHAT_VERSION}.exe WeChatSetup.exe
RUN chown user:group WeChatSetup.exe && chmod a+x WeChatSetup.exe

# Download wxhelper
# https://github.com/ttttupup/wxhelper
ADD https://github.com/ttttupup/wxhelper/releases/download/${WECHAT_VERSION}-v${WXHELPER_VERSION}/wxhelper.dll wxhelper.dll
RUN chown user:group wxhelper.dll

# Debug info
RUN ls -lah

# Install WeChat
COPY install-wechat.sh install-wechat.sh
RUN chmod a+x install-wechat.sh
USER user
RUN ./install-wechat.sh && rm -rf WeChatSetup.exe && rm -rf install-wechat.sh

# Disable crash dialog and update prompt
RUN before=$(stat -c '%Y' /home/user/.wine64/user.reg) \
	&& wine64 reg add 'HKEY_CURRENT_USER\Software\Wine\WineDbg' /v ShowCrashDialog /t REG_DWORD /d 0 \
	&& wine64 reg add 'HKEY_CURRENT_USER\Software\Tencent\WeChat' /v NeedUpdateType /t REG_DWORD /d 0 \
	&& while [ $(stat -c '%Y' /home/user/.wine64/user.reg) = $before ]; do sleep 1; done

# Add WeChat agent
COPY --from=builder /build/matrix-wechat-agent.exe /home/user/matrix-wechat-agent/matrix-wechat-agent.exe
COPY ./scripts/run.py /usr/bin/run.py
USER root
RUN set -ex && \
	chmod a+x /usr/bin/run.py && \
	apt-get autoremove -y && \
	apt-get clean && \
	rm -fr /tmp/*

# Workaround for WeChat crashing on startup
# https://github.com/sandboxie-plus/Sandboxie/issues/2674#issuecomment-1425317945
# https://github.com/vufa/deepin-wine-wechat-arch/issues/270
ADD https://github.com/vufa/deepin-wine-wechat-arch/raw/action/mmmojo.dll mmmojo.dll
ADD https://github.com/vufa/deepin-wine-wechat-arch/raw/action/mmmojo_64.dll mmmojo_64.dll
RUN chown user:group mmmojo.dll	\
	&& chown user:group mmmojo_64.dll \
	&& cp mmmojo.dll /home/user/.wine64/drive_c/Program\ Files/Tencent/WeChat/\[3.9.10.19\]/mmmojo.dll \
	&& cp mmmojo_64.dll /home/user/.wine64/drive_c/Program\ Files/Tencent/WeChat/\[3.9.10.19\]/mmmojo_64.dll

# Add injector (for manual testing)
#ADD https://github.com/ttttupup/wxhelper/blob/main/tool/injector/ConsoleApplication.exe ConsoleApplication.exe
#RUN chown user:group ConsoleApplication.exe

USER user
WORKDIR /home/user/matrix-wechat-agent

ENTRYPOINT ["/usr/bin/dumb-init", "--"]
CMD ["/usr/bin/run.py"]
