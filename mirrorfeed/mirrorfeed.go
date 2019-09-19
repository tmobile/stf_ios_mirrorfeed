package main

import (
	"fmt"
	"html/template"
	"image"
	_ "image/jpeg"
    "io"
    "log"
    "net"
    "net/http"
    "os"
    "sync"
  
    "./mods/mjpeg"
    "github.com/gorilla/websocket"
)

var listen_addr = "localhost:8000"

func callback( r *http.Request ) bool {
    return true
}

var upgrader = websocket.Upgrader {
    CheckOrigin: callback,
}

type ImgMsg struct {
    imgNum int
    msg string
}

type ImgType struct {
    rawData []byte
}

const (
    WriterStop = iota
)

type WriterMsg struct {
    msg int
}

const (
    DummyStart = iota
    DummyStop
)

type DummyMsg struct {
    msg int
}

func main() {
	mirrorPort := os.Args[1]
	listen_addr = fmt.Sprintf( "0.0.0.0:%s", mirrorPort )
	
    filename := os.Args[2]
    
    var fd *os.File
    var err error
    
    ifaces, err := net.Interfaces()
    if err != nil {
        fmt.Printf( err.Error() )
        os.Exit( 1 )
    }
    for _, iface := range ifaces {
        //fmt.Printf("Found interface: %s\n", iface.Name )
        addrs, err := iface.Addrs()
        if err != nil {
            fmt.Printf( err.Error() )
            os.Exit( 1 )
        }
        for _, addr := range addrs {
            var ip net.IP
            switch v := addr.(type) {
                case *net.IPNet:
                    ip = v.IP
                case *net.IPAddr:
                    ip = v.IP
                default:
                    fmt.Printf("Unknown type\n")
            }
            if iface.Name == "utun1" {
                fmt.Printf( "utun1 address: %s\n", ip.String() ) 
                listen_addr = ip.String() + ":" + mirrorPort
                //listen_addr = "0.0.0.0:" + mirrorPort
            }
        }
    }
  
    imgCh := make(chan ImgMsg, 10)
    dummyCh := make(chan DummyMsg, 2)
    //var imgs map[int]ImgType
    imgs := make(map[int]ImgType)
    lock := sync.RWMutex{}
    
    go dummyReceiver( imgCh, dummyCh, imgs, &lock )
        
    go func() {
        for {
            fmt.Printf("Opening incoming video: %s\n", filename)
            //if filename == "pipe" {
                fd, _ = os.OpenFile(filename, os.O_RDONLY, 0600)
            /*} else {
                fd, err = os.Open(os.Args[1])
                if err != nil {
                    log.Fatalln(err)
                }
            }*/
            sc := mjpeg.Open(fd)
            
            imgnum := 1
            
            fmt.Printf("Receiving incoming video\n")
            for sc.Scan() {
                err := sc.Err()
                if err != nil && err != io.EOF {
                    log.Fatalln(err)
                }
                
                rawData := sc.FrameRaw() 
                
                img := ImgType{}
                img.rawData = rawData.Bytes()
                
                lock.Lock()
                imgs[imgnum] = img
                lock.Unlock()
                
                config, _, _ := image.DecodeConfig( rawData ) // ( config, format, error )
                msg := fmt.Sprintf("Width: %d, Height: %d, Size: %d\n", config.Width, config.Height, rawData.Len() )
                imgMsg := ImgMsg{}
                imgMsg.imgNum = imgnum
                imgMsg.msg = msg
                imgCh <- imgMsg

                //fmt.Printf("Width: %d, Height: %d, Size: %d\n", config.Width, config.Height, rawData.Len() )
                imgnum++
            }
        }
    }()
    
    startServer( imgCh, dummyCh, imgs, &lock )
}

func dummyReceiver(imgCh <-chan ImgMsg,dummyCh <-chan DummyMsg,imgs map[int]ImgType, lock *sync.RWMutex) {
    var running bool = true
    for {
        if running == true {
            for {
                select {
                    case imgMsg := <- imgCh:
                        // dump the image
                        lock.Lock()
                        delete(imgs,imgMsg.imgNum)
                        lock.Unlock()
                        //fmt.Printf("X.");
                    case controlMsg := <- dummyCh:
                        if controlMsg.msg == DummyStop {
                            running = false
                        }
                }
                if running == false {
                    break
                }
            }
        } else {
            controlMsg := <- dummyCh
            if controlMsg.msg == DummyStart {
                running = true
            }
        }
    }
}

func writer(ws *websocket.Conn,imgCh <-chan ImgMsg,writerCh <-chan WriterMsg,imgs map[int]ImgType,lock *sync.RWMutex) {
    var running bool = true
    for {
        select {
            case imgMsg := <- imgCh:
                imgNum := imgMsg.imgNum
                // Keep receiving images till there are no more to receive
                for {
                    if len(imgCh) == 0 {
                        break
                    }
                    lock.Lock()
                    delete(imgs,imgNum)
                    lock.Unlock()
                    imgMsg = <- imgCh
                }
        
                lock.Lock()
                img := imgs[imgNum]
                lock.Unlock();
                        
                ws.WriteMessage(websocket.TextMessage, []byte(imgMsg.msg))
                bytes := img.rawData
                
                ws.WriteMessage(websocket.BinaryMessage, bytes )
                
                lock.Lock()
                delete(imgs,imgNum)
                lock.Unlock()
                
            case controlMsg := <- writerCh:
                if controlMsg.msg == WriterStop {
                    running = false
                }
        }
        if running == false {
            break
        }
    }
}

func startServer( imgCh <-chan ImgMsg, dummyCh chan<- DummyMsg, imgs map[int]ImgType, lock *sync.RWMutex ) {
    fmt.Printf("Listening on %s\n", listen_addr )
    
    echoClosure := func( w http.ResponseWriter, r *http.Request ) {
        handleEcho( w, r, imgCh, dummyCh, imgs, lock )
    }
    http.HandleFunc( "/echo", echoClosure )
    http.HandleFunc( "/echo/", echoClosure )
    http.HandleFunc( "/", handleRoot )
    log.Fatal( http.ListenAndServe( listen_addr, nil ) )
}

func handleEcho( w http.ResponseWriter, r *http.Request,imgCh <-chan ImgMsg,dummyCh chan<- DummyMsg,imgs map[int]ImgType,lock *sync.RWMutex) {
    c, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Print("Upgrade error:", err)
        return
    }
    defer c.Close()
    fmt.Printf("Received connection\n")
    welcome(c)
        
    writerCh := make(chan WriterMsg, 2)
    
    // stop Dummy Reader from sucking up images
    startMsg := DummyMsg{}
    startMsg.msg = DummyStop
    dummyCh <- startMsg
    
    go writer(c,imgCh,writerCh,imgs,lock)
    for {
        mt, message, err := c.ReadMessage()
        if err != nil {
            log.Println("read:", err)
            break
        }
        log.Printf("recv: %s", message)
        err = c.WriteMessage(mt, message)
        if err != nil {
            log.Println("write:", err)
            break
        }
    }
    
    // send WriterMsg to terminate writer
    stopMsg := WriterMsg{}
    stopMsg.msg = WriterStop
    writerCh <- stopMsg
    
    // trigger Dummy Reader to begin again
    startMsg = DummyMsg{}
    startMsg.msg = DummyStart
    dummyCh <- startMsg
}

func welcome( c *websocket.Conn ) ( error ) {
    msg := `
{
    "version":1,
    "length":24,
    "pid":12733,
    "realWidth":750,
    "realHeight":1334,
    "virtualWidth":375,
    "virtualHeight":667,
    "orientation":0,
    "quirks":{
        "dumb":false,
        "alwaysUpright":true,
        "tear":false
    }
}`
    return c.WriteMessage( websocket.TextMessage, []byte(msg) )
}

func handleRoot( w http.ResponseWriter, r *http.Request ) {
    rootTpl.Execute( w, "ws://"+r.Host+"/echo" )
}

var rootTpl = template.Must(template.New("").Parse(`
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
	canvas {
		border: solid 1px black;
	}
</style>
<script>
	function getel( id ) {
		return document.getElementById( id );
	}
	
	window.addEventListener("load", function(evt) {
		var output = getel("output");
		var input  = getel("input");
		var ctx    = getel("canvas").getContext("2d");
		var ws;
		
		getel("open").onclick = function( event ) {
			if( ws ) {
				return false;
			}
			ws = new WebSocket("{{.}}");
			ws.onopen = function( event ) {
				console.log("Websocket open");
			}
			ws.onclose = function( event ) {
				console.log("Websocket closed");
				ws = null;
			}
			ws.onmessage = function( event ) {
				if( event.data instanceof Blob ) {
					var image = new Image();
					var url;
					image.onload = function() {
						ctx.drawImage(image, 0, 0);
						URL.revokeObjectURL( url );
					};
					image.onerror = function( e ) {
						console.log('Error during loading image:', e);
					}
					var blob = event.data;
					
					url = URL.createObjectURL( blob );
					image.src = url;
				}
				else {
					var text = "Response: " + event.data;
					var d = document.createElement("div");
					d.innerHTML = text;
					output.appendChild( d );
				}
			}
			ws.onerror = function( event ) {
				console.log( "Error: ", event.data );
			}
			return false;
		};
		getel("send").onclick = function( event ) {
			if( !ws ) return false;
			ws.send( input.value );
			return false;
		};
		getel("close").onclick = function( event)  {
			if(!ws) return false;
			ws.close();
			return false;
		};
	});
</script>
</head>
<body>
	<table>
		<tr>
			<td valign="top">
				<canvas id="canvas" width="750" height="1334"></canvas>
			</td>
			<td valign="top" width="50%">
				<form>
					<button id="open">Open</button>
					<button id="close">Close</button>
					<br>
					<input id="input" type="text" value="">
					<button id="send">Send</button>
				</form>
			</td>
			<td valign="top" width="50%">
				<div id="output"></div>
			</td>
		</tr>
	</table>
</body>
</html>
`))