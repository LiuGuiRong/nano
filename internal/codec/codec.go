package codec

import (
	"bytes"
	"errors"

	"github.com/lonng/nano/internal/packet"
)

// 编解码器常量
const (
	HeadLength    = 4
	MaxPacketSize = 64 * 1024
)

// 定义错误变量
var ErrPacketSizeExcced = errors.New("codec: packet size exceed")

// 解码器结构体
type Decoder struct {
	buf  *bytes.Buffer // 缓冲区
	size int           // 上一个数据包的长度
	typ  byte          // 上一个数据包的类型
}

// 新建解码器
func NewDecoder() *Decoder {
	return &Decoder{
		buf:  bytes.NewBuffer(nil),
		size: -1,
	}
}

// 前进到下一个数据包
func (c *Decoder) forward() error {
	header := c.buf.Next(HeadLength) // 读取头部
	c.typ = header[0]                // 获取数据包类型
	if c.typ < packet.Handshake || c.typ > packet.Kick {
		return packet.ErrWrongPacketType // 错误的数据包类型
	}
	c.size = bytesToInt(header[1:]) // 获取数据包长度

	// 数据包长度限制
	if c.size > MaxPacketSize {
		return ErrPacketSizeExcced // 数据包长度超出限制
	}
	return nil
}

// 解码网络字节切片
func (c *Decoder) Decode(data []byte) ([]*packet.Packet, error) {
	c.buf.Write(data) // 写入数据到缓冲区

	var (
		packets []*packet.Packet
		err     error
	)
	// 检查长度
	if c.buf.Len() < HeadLength {
		return nil, err // 数据不足
	}

	// 第一次解码
	if c.size < 0 {
		if err = c.forward(); err != nil {
			return nil, err // 前进到下一个数据包
		}
	}

	for c.size <= c.buf.Len() {
		p := &packet.Packet{Type: packet.Type(c.typ), Length: c.size, Data: c.buf.Next(c.size)}
		packets = append(packets, p) // 添加数据包到结果集

		// 更多的数据包
		if c.buf.Len() < HeadLength {
			c.size = -1
			break
		}

		if err = c.forward(); err != nil {
			return packets, err // 前进到下一个数据包
		}
	}

	return packets, nil
}

// 编码数据包
func Encode(typ packet.Type, data []byte) ([]byte, error) {
	if typ < packet.Handshake || typ > packet.Kick {
		return nil, packet.ErrWrongPacketType // 错误的数据包类型
	}

	p := &packet.Packet{Type: typ, Length: len(data)}
	buf := make([]byte, p.Length+HeadLength)
	buf[0] = byte(p.Type)

	copy(buf[1:HeadLength], intToBytes(p.Length)) // 复制长度到缓冲区
	copy(buf[HeadLength:], data)                  // 复制数据到缓冲区

	return buf, nil
}

// 将字节数组转换为整数
func bytesToInt(b []byte) int {
	result := 0
	for _, v := range b {
		result = result<<8 + int(v) // 大端序转换
	}
	return result
}

// 将整数转换为字节数组
func intToBytes(n int) []byte {
	buf := make([]byte, 3)
	buf[0] = byte((n >> 16) & 0xFF)
	buf[1] = byte((n >> 8) & 0xFF)
	buf[2] = byte(n & 0xFF)
	return buf
}
