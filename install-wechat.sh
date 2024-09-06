#!/usr/bin/env bash

# Start Xvfb in the background to create a virtual display (for the installer)
Xvfb :99 -screen 0 1024x768x16 &
XVFB_PID=$!
export DISPLAY=:99
sleep 5

# ## https://gitlab.com/cunidev/gestures/-/wikis/xdotool-list-of-key-codes
# function install() {

#     for i in {1..10}; do
#         # List all windows
#         for window in $(xdotool search --name ""); do
#             window_name=$(xdotool getwindowname $window)
#             echo "Window ID: $window, Window Name: $window_name"
#         done

#         # Find the WeChat Setup window
#         WINDOW=$(xdotool search --name "WeChat Setup")
#         if [ -n "$WINDOW" ]; then
#             echo "Found WeChat Setup window ID: $WINDOW"
#             sleep 20 # Wait for the installer to start
#             WINDOW_INFO=$(xwininfo -id $WINDOW)
#             echo "Window info for WeChat Setup: $WINDOW_INFO"

#             xwd -silent -id $WINDOW -out wechat_setup.xwd
#             xdotool windowfocus $WINDOW
#             sleep 1
#             xwd -silent -id $WINDOW -out wechat_setup2.xwd

#             xdotool key Tab
#             sleep 0.5
#             xdotool key Tab
#             sleep 0.5
#             xdotool key Tab
#             sleep 0.5
#             xdotool key space
#             sleep 0.5
#             xdotool key Tab
#             sleep 0.5
#             xdotool key Tab
#             sleep 0.5
#             xdotool key Tab
#             sleep 0.5
#             xdotool key Tab
#             sleep 0.5
#             xdotool key Tab
#             sleep 0.5
#             xdotool key Tab
#             sleep 0.5
#             xdotool key Tab
#             sleep 0.5
#             xdotool key Return
#             sleep 16
#             xdotool key Tab
#             sleep 0.5
#             xdotool key Tab
#             sleep 0.5
#             xdotool key Tab
#             sleep 0.5
#             xdotool key Return
#             echo "WeChat installation started"
#             break
#         else
#             echo "WeChat Setup window not found"
#         fi
#         sleep 10
#     done
# }

echo "Starting WeChat installation"
wine64 'C:\WeChatSetup.exe /S'
#WECHAT_PID=$!

#sleep 60 # Wait for the installer to start and unpack
#install

#sleep 120 # Wait for the actual installation to complete
# for i in {1..15}; do
#     # List all windows
#     for window in $(xdotool search --name ""); do
#         window_name=$(xdotool getwindowname $window)
#         echo "Window ID: $window, Window Name: $window_name"
#     done

#     # Find the WeChat window
#     WINDOW=$(xdotool search --name "WeChat")
#     if [ -n "$WINDOW" ]; then
#         echo "Found WeChat window ID: $WINDOW"
#     else
#         echo "WeChat window not found"
#         break
#     fi
#     sleep 20
# done
#wait $WECHAT_PID
echo "WeChat installation completed"

sleep 10

# Kill the Xvfb process
kill $XVFB_PID
