TARGET = ../../../bin/mirrorfeed

all: $(TARGET)

$(TARGET): mirrorfeed.go go.sum
	go build  -o $(TARGET) mirrorfeed.go

go.sum:
	go get
	go get .
	go get ./mods/mjpeg/.

clean:
	$(RM) $(TARGET) go.sum