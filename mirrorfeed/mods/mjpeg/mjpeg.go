// mjpeg stream to frame decoder
package mjpeg

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"io"
)

func Open(r io.Reader) *Scanner {
	return &Scanner{
		br: bufio.NewReader(r),
	}
}

type Scanner struct {
	hdr   HdrA
	br    *bufio.Reader
	frame *bytes.Buffer
	err   error
}

func (s *Scanner) Scan() bool {
	hdr := HdrA{}
	err := binary.Read(s.br, binary.BigEndian, &hdr)
	if err != nil {
		s.err = err
		return false
	}
	if hdr.SOI != 0xffd8 {
		s.err = errors.New("soi incorrect")
		return false
	}
	if hdr.APP0 != 0xffe0 {
		s.err = errors.New("app0 incorrect")
		fmt.Printf("APP0:%#x\n", hdr.APP0)
		return false
	}
	if hdr.Ident != 0x4a464946 { // JFIF
		s.err = errors.New("ident incorrect")
		fmt.Printf("Ident:%#x\n", hdr.Ident)
		return false
	}
	// fmt.Printf("JFIF Version:%d.%d\n", hdr.JFIFv1, hdr.JFIFv2)
	
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, hdr)
	
	var done bool
	for true {
		block := BlockType{}
		err := binary.Read(s.br, binary.BigEndian, &block)
		if err != nil {
			return false
		}
		binary.Write(buf, binary.BigEndian, &block)
		
		if block.Type == 0xffd9 {
			break
		}
		
		block2 := BlockLen{}
		err2 := binary.Read(s.br, binary.BigEndian, &block2)
		if err2 != nil {
			s.err = err2
			return false
		}
		binary.Write(buf, binary.BigEndian, &block2)
		
		if block.Type == 0xffc4 { // huffman
			//fmt.Printf("Huffman table - len: %d\n", block2.Len)
			io.CopyN(buf, s.br, int64(block2.Len-2))
		} else if block.Type == 0xffc0 { // frame
			//fmt.Printf("Frame - len: %d\n", block2.Len)
			io.CopyN(buf, s.br, int64(block2.Len-2))
		} else if block.Type == 0xfffe { // user block
			//fmt.Printf("User block - len: %d\n", block2.Len)
			io.CopyN(buf, s.br, int64(block2.Len-2))
		} else if block.Type == 0xffda { // scan block
			//fmt.Printf("Scan block - len: %d\n", block2.Len)
			io.CopyN(buf, s.br, int64(block2.Len-2))
			
			// Read bytes one at a time until we encounter 0xffd9 ( end of image )
			var p [1]byte
			var b byte
			var prev byte
			for true {
				_, err = s.br.Read(p[:])
				if err != nil {
					break
				}
				prev = b
				b = p[0]
				if b == 0xd9 && prev == 0xff {
					buf.Write(p[:])
					done = true
					break
			    }
			    buf.Write(p[:])
			}	
			if done {
				break
			}
		} else if block.Type == 0xffdb { // quant block
			//fmt.Printf("Quant block - len: %d\n", block2.Len)
			io.CopyN(buf, s.br, int64(block2.Len-2))
		} else {
			fmt.Printf("Unknown block type:%#x\n", block.Type)
			//io.CopyN(buf, s.br, int64(block.Length-2))
		}
    }
	
	s.frame = buf
	return true
}

func (s *Scanner) FrameRaw() *bytes.Buffer {
	return s.frame
}

func (s *Scanner) FrameImage() (img image.Image, format string, error error) {
	return image.Decode(bytes.NewReader(s.frame.Bytes()))
}

func (s *Scanner) Err() error {
	return s.err
}

type HdrA struct {
	SOI         uint16
	APP0        uint16
	Length      uint16
	Ident       uint32
	Null        uint8
	JFIFv1      uint8
	JFIFv2      uint8
	Density     uint8
	XDensity    uint16
	YDensity    uint16
	XThumbnail  uint8
	YThumbnail  uint8
}

type BlockType struct {
	Type uint16
}

type BlockLen struct {
	Len uint16
}