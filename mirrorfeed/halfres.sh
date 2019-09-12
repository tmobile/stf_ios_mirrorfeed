#!/bin/bash
echo "Getting video from $1"
echo "Sending video to $2"
~/proj/ffmpeg/ffmpeg/ffmpeg -f avfoundation -pixel_format uyvy422 -i "$1" -f mjpeg -bsf:v mjpegadump -bsf:v mjpeg2jpeg -vf "framestep=10,scale=375x667" -qscale:v 18 pipe:1 > $2
