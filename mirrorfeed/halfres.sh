#!/bin/bash
echo "Getting video from $1"
echo "Sending video to $2"
~/proj/ffmpeg/ffmpeg/ffmpeg -f avfoundation -pixel_format bgr0 -i "cx iPhone" -f mjpeg -bsf:v mjpegadump -bsf:v mjpeg2jpeg -r 1 -vsync 2 pipe:1 > $2
