#!/bin/bash
~/proj/ffmpeg/ffmpeg/ffmpeg -f avfoundation -pixel_format uyvy422 -i "cxs phone" -f mjpeg -bsf:v mjpegadump -bsf:v mjpeg2jpeg -filter:v framestep=10 -qscale:v 18 pipe:1 > imgpipe
