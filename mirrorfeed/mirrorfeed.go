package main

import (
    "encoding/json"
    "fmt"
    "html/template"
    "image"
    _ "image/jpeg"
    "io"
    "net"
    "net/http"
    "os"
    "strconv"
    "sync"
    "time"
    
    "github.com/tmobile/stf_ios_mirrorfeed/mirrorfeed/mods/mjpeg"
    "github.com/gorilla/websocket"
    zmq "github.com/zeromq/goczmq"
    log "github.com/sirupsen/logrus"
)

var listen_addr = "localhost:8000"
var reqSock *zmq.Sock
var reqOb *zmq.ReadWriter

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

type Stats struct {
    recv int
    dumped int
    sent int
    dummyActive bool
    socketConnected bool
    waitCnt int
    width int
    height int
}

func ifAddr( ifName string ) ( addrOut string ) {
    ifaces, err := net.Interfaces()
    if err != nil {
        fmt.Printf( err.Error() )
        os.Exit( 1 )
    }
    
    addrOut = ""
    for _, iface := range ifaces {
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
            if iface.Name == ifName {
                addrOut = ip.String()
            }
        }
    }
    return addrOut
}

func main() {
    mirrorPort := os.Args[1]
    listen_addr = fmt.Sprintf( "0.0.0.0:%s", mirrorPort )
  
    filename := os.Args[2]
    
    tunName := os.Args[3]
    
    uuid := os.Args[4]
    
    var fd *os.File
    var err error
    
    if tunName == "none" {
        fmt.Printf("No tunnel specified; listening on all interfaces\n")
    } else {
        addr := ifAddr( tunName )
        if addr != "" {
            listen_addr = addr + ":" + mirrorPort
        } else {
            fmt.Printf( "Could not find interface %s\n", tunName )
            os.Exit( 1 )
        }
    }
  
    imgCh := make(chan ImgMsg, 10)
    dummyCh := make(chan DummyMsg, 2)
    
    imgs := make(map[int]ImgType)
    lock := sync.RWMutex{}
    var stats Stats = Stats{
        width: 0,
        height: 0,
    }
    stats.recv = 0
    stats.dumped = 0
    stats.dummyActive = false
    stats.socketConnected = false
    
    statLock := sync.RWMutex{}
    var dummyRunning = false
    
    setup_zmq()
    
    go dummyReceiver( imgCh, dummyCh, imgs, &lock, &statLock, &stats, &dummyRunning )
        
    go func() {
        for {
            fmt.Printf("Opening incoming video: %s\n", filename)
            
            fd, err = os.OpenFile(filename, os.O_RDONLY, 0600)
            
            if err != nil {
                fmt.Println(err)
                time.Sleep( time.Second * 1 )
                continue                
            }
            
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
                
                statLock.Lock()
                if( stats.width == 0 ) {
                    stats.width = config.Width
                    stats.height = config.Height
                    msgCoord( map[string]string{
                      "type": "mirrorfeed_dimensions",
                      "width": strconv.Itoa( config.Width ),
                      "height": strconv.Itoa( config.Height ),
                      "uuid": uuid,
                    } )
                }
                stats.recv++
                statLock.Unlock()

                imgnum++
            }
            
            time.Sleep( time.Second * 2 )
        }
    }()
    
    startServer( imgCh, dummyCh, imgs, &lock, &statLock, &stats, &dummyRunning )
    
    close_zmq()
}

func dummyReceiver(imgCh <-chan ImgMsg,dummyCh <-chan DummyMsg,imgs map[int]ImgType, lock *sync.RWMutex, statLock *sync.RWMutex, stats *Stats, dummyRunning *bool ) {
    statLock.Lock()
    stats.dummyActive = true
    statLock.Unlock()
    var running bool = true
    *dummyRunning = true
    for {
        if running == true {
            for {
                select {
                    case imgMsg := <- imgCh:
                        // dump the image
                        lock.Lock()
                        delete(imgs,imgMsg.imgNum)
                        lock.Unlock()
                        statLock.Lock()
                        stats.dumped++
                        statLock.Unlock()
                    case controlMsg := <- dummyCh:
                        if controlMsg.msg == DummyStop {
                            running = false
                            lock.Lock()
                            *dummyRunning = false
                            lock.Unlock()
                            statLock.Lock()
                            stats.dummyActive = false
                            statLock.Unlock()
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
                lock.Lock()
                *dummyRunning = true
                lock.Unlock()
                statLock.Lock()
                stats.dummyActive = true
                statLock.Unlock()
            }
        }
    }
}

func writer(ws *websocket.Conn,imgCh <-chan ImgMsg,writerCh <-chan WriterMsg,imgs map[int]ImgType,lock *sync.RWMutex, statLock *sync.RWMutex, stats *Stats) {
    var running bool = true
    statLock.Lock()
    stats.socketConnected = false
    statLock.Unlock()
    
    //var prevImg *image.RGBA
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
                lock.Unlock()
                
                lock.Lock()
                ws.WriteMessage(websocket.TextMessage, []byte(imgMsg.msg))
                bytes := img.rawData
                
                ws.WriteMessage(websocket.BinaryMessage, bytes )
                lock.Unlock()
                
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
    statLock.Lock()
    stats.socketConnected = false
    statLock.Unlock()
}

func startServer( imgCh <-chan ImgMsg, dummyCh chan<- DummyMsg, imgs map[int]ImgType, lock *sync.RWMutex, statLock *sync.RWMutex, stats *Stats, dummyRunning *bool ) {
    fmt.Printf("Listening on %s\n", listen_addr )
    
    echoClosure := func( w http.ResponseWriter, r *http.Request ) {
        handleEcho( w, r, imgCh, dummyCh, imgs, lock, statLock, stats, dummyRunning )
    }
    statsClosure := func( w http.ResponseWriter, r *http.Request ) {
        handleStats( w, r, statLock, stats )
    }
    http.HandleFunc( "/echo", echoClosure )
    http.HandleFunc( "/echo/", echoClosure )
    http.HandleFunc( "/", handleRoot )
    http.HandleFunc( "/stats", statsClosure )
    log.Fatal( http.ListenAndServe( listen_addr, nil ) )
}

func handleStats( w http.ResponseWriter, r *http.Request, statLock *sync.RWMutex, stats *Stats ) {
    statLock.Lock()
    recv := stats.recv
    dumped := stats.dumped
    dummyActive := stats.dummyActive
    socketConnected := stats.socketConnected
    statLock.Unlock()
    waitCnt := stats.waitCnt
    
    var dummyStr string = "no"
    if dummyActive {
        dummyStr = "yes"
    }
    
    var socketStr string = "no"
    if socketConnected {
        socketStr = "yes"
    }
    
    fmt.Fprintf( w, "Received: %d<br>\nDumped: %d<br>\nDummy Active: %s<br>\nSocket Connected: %s<br>\nWait Count: %d<br>\n", recv, dumped, dummyStr, socketStr, waitCnt )
}

func handleEcho( w http.ResponseWriter, r *http.Request,imgCh <-chan ImgMsg,dummyCh chan<- DummyMsg,imgs map[int]ImgType,lock *sync.RWMutex, statLock *sync.RWMutex, stats *Stats, dummyRunning *bool) {
    c, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Print("Upgrade error:", err)
        return
    }
    defer c.Close()
    fmt.Printf("Received connection\n")
    
    statLock.Lock()
    width  := stats.width
    height := stats.height
    statLock.Unlock()
    
    welcome(c, width, height)
        
    writerCh := make(chan WriterMsg, 2)
    
    // stop Dummy Reader from sucking up images
    stopMsg1 := DummyMsg{}
    stopMsg1.msg = DummyStop
    dummyCh <- stopMsg1
    
    // ensure the Dummy Reader has stopped
    for {
        lock.Lock()
        if !*dummyRunning {
            lock.Unlock()
            break
        }
        lock.Unlock()
        time.Sleep( time.Second * 1 )
        statLock.Lock()
        stats.waitCnt++
        statLock.Unlock()
    }
    
    go writer(c,imgCh,writerCh,imgs,lock,statLock,stats)
    for {
        mt, message, err := c.ReadMessage()
        if err != nil {
            log.Println("read:", err)
            break
        }
        log.Printf("recv: %s", message)
        lock.Lock()
        err = c.WriteMessage(mt, message)
        lock.Unlock()
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
    startMsg := DummyMsg{}
    startMsg.msg = DummyStart
    dummyCh <- startMsg
}

func welcome( c *websocket.Conn, width int, height int ) ( error ) {
    msg := fmt.Sprintf( `
{
    "version":1,
    "length":24,
    "pid":12733,
    "realWidth":%d,
    "realHeight":%d,
    "virtualWidth":%d,
    "virtualHeight":%d,
    "orientation":0,
    "quirks":{
        "dumb":false,
        "alwaysUpright":true,
        "tear":false
    }
}`, width, height, width, height )
    return c.WriteMessage( websocket.TextMessage, []byte(msg) )
}

func handleRoot( w http.ResponseWriter, r *http.Request ) {
    rootTpl.Execute( w, "ws://"+r.Host+"/echo" )
}

func setup_zmq() {
    reqSock = zmq.NewSock(zmq.Push)
    
    spec := "tcp://127.0.0.1:7300"
    reqSock.Connect( spec )

    var err error
    reqOb, err = zmq.NewReadWriter(reqSock)
    if err != nil {
        log.WithFields( log.Fields{
            "type": "zmq_connect_err",
            "err": err,
        } ).Error("ZMQ Send Error")
    }
    
    reqOb.SetTimeout(1000)
    
    zmqRequest( []byte("dummy") )
}

func close_zmq() {
    reqSock.Destroy()
    reqOb.Destroy()
}

func msgCoord( content map[string]string ) {
    data, _ := json.Marshal( content )
    zmqRequest( data )
}

func zmqRequest( jsonOut []byte ) {
    err := reqSock.SendMessage( [][]byte{ jsonOut } )
    if err != nil {
        log.WithFields( log.Fields{
            "type": "zmq_send_err",
            "err": err,
        } ).Error("ZMQ Send Error")
    }
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