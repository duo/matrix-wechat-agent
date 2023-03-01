#!/usr/bin/python3

import logging
import os
import signal
import subprocess

logger = logging.getLogger("matrix")
logger.setLevel(logging.DEBUG)
logger.addHandler(logging.StreamHandler())


class AgentManager:
    def __init__(self):
        signal.signal(signal.SIGINT, self.stop)
        signal.signal(signal.SIGHUP, self.stop)
        signal.signal(signal.SIGTERM, self.stop)

    def stop(self, signum, frame):
        logger.info("Stop agent...")
        try:
            if self.agent:
                os.kill(self.agent.pid, signal.SIGTERM)
        except Exception as e:
            logger.error('Error at stop agent', exc_info=e)
        logger.info("Stop vnc...")
        try:
            if self.vnc:
                os.kill(self.vnc.pid, signal.SIGTERM)
        except Exception as e:
            logger.error('Error at stop vnc', exc_info=e)
        try:
            subprocess.run(['sudo', 'rm', '-f', '/tmp/.X5-lock'])
            subprocess.run(['sudo', 'rm', '-f', '/tmp/.X11-unix/X5'])
        except Exception as e:
            logger.error('Error at cleanup X', exc_info=e)

    def start_vnc(self):
        logger.info("Start vnc...")
        os.makedirs('/home/user/.vnc', mode=755, exist_ok=True)
        passwd_output = subprocess.run(
            ['/usr/bin/vncpasswd', '-f'], input=os.environ['VNCPASS'].encode(), capture_output=True)
        with open('/home/user/.vnc/passwd', 'wb') as f:
            f.write(passwd_output.stdout)
        os.chmod('/home/user/.vnc/passwd', 0o700)
        self.vnc = subprocess.Popen(['/usr/bin/vncserver', '-localhost',
                                     'no', '-xstartup', '/usr/bin/openbox', ':5'])

    def start_agent(self):
        logger.info("Start agent...")
        self.agent = subprocess.Popen(['wine', 'cmd', '/k',
                                       '/home/user/matrix-wechat-agent/matrix-wechat-agent.exe'])
        self.agent.wait()

    def start(self):
        self.start_vnc()
        self.start_agent()


if __name__ == '__main__':
    logger.info("Start agent manager...")

    manager = AgentManager()
    manager.start()
