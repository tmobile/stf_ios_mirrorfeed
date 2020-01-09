module github.com/tmobile/stf_ios_mirrorfeed/mirrorfeed

go 1.12

require github.com/gorilla/websocket v1.4.1

require (
	github.com/sirupsen/logrus v1.4.2 // indirect
	github.com/tmobile/stf_ios_mirrorfeed/mirrorfeed/mods/mjpeg v1.0.0
	github.com/zeromq/goczmq v4.1.0+incompatible // indirect
)

replace github.com/tmobile/stf_ios_mirrorfeed/mirrorfeed/mods/mjpeg v1.0.0 => ./mods/mjpeg
