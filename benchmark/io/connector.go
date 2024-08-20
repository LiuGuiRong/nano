// nano 作者版权所有。保留所有权利。
//
// 特此免费授予任何获得本软件及相关文档文件（“软件”）副本的人员使用、复制、修改、合并、发布、分发、再许可和/或出售软件副本的权限，并允许向其提供软件的人员这样做，但须符合以下条件：
//
// 上述版权声明和本许可声明应包含在软件的所有副本或主要部分中。
//
// 本软件按“原样”提供，不提供任何明示或暗示的担保，包括但不限于对适销性、特定用途适用性和非侵权的担保。在任何情况下，作者或版权持有人均不对因本软件或本软件的使用或其他交易而产生的任何索赔、损害或其他责任负责，无论是在合同诉讼、侵权诉讼或其他诉讼中。

package io

import (
	"log"
	"net"
	"sync"

	"github.com/lonng/nano/internal/codec"
	"github.com/lonng/nano/internal/message"
	"github.com/lonng/nano/internal/packet"
	"google.golang.org/protobuf/proto"
)

var (
	hsd []byte // 握手数据
	had []byte // 握手确认数据
)

func init() {
	var err error
	hsd, err = codec.Encode(packet.Handshake, nil)
	if err != nil {
		panic(err)
	}

	had, err = codec.Encode(packet.HandshakeAck, nil)
	if err != nil {
		panic(err)
	}
}

type (
	// Callback 表示在相应事件发生时将被调用的回调类型
	Callback func(data interface{})

	// Connector 是一个小型的 Nano 客户端
	Connector struct {
		conn   net.Conn       // 底层连接
		codec  *codec.Decoder // 解码器
		die    chan struct{}  // 连接关闭通道
		chSend chan []byte    // 发送队列
		mid    uint64         // 消息ID

		// 事件处理器
		muEvents sync.RWMutex
		events   map[string]Callback

		// 响应处理器
		muResponses sync.RWMutex
		responses   map[uint64]Callback

		connectedCallback func() // 连接成功回调
	}
)

// NewConnector 创建一个新的 Connector 实例
func NewConnector() *Connector {
	return &Connector{
		die:       make(chan struct{}),
		codec:     codec.NewDecoder(),
		chSend:    make(chan []byte, 64),
		mid:       1,
		events:    map[string]Callback{},
		responses: map[uint64]Callback{},
	}
}

// Start 连接到服务器并在客户端和服务器之间发送/接收数据
func (c *Connector) Start(addr string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}

	c.conn = conn

	go c.write()

	// 发送握手数据包
	c.send(hsd)

	// 读取并处理网络消息
	go c.read()

	return nil
}

// OnConnected 设置在客户端连接到服务器时将被调用的回调函数
func (c *Connector) OnConnected(callback func()) {
	c.connectedCallback = callback
}

// Request 发送请求到服务器并为响应注册回调
func (c *Connector) Request(route string, v proto.Message, callback Callback) error {
	data, err := serialize(v)
	if err != nil {
		return err
	}

	msg := &message.Message{
		Type:  message.Request,
		Route: route,
		ID:    c.mid,
		Data:  data,
	}

	c.setResponseHandler(c.mid, callback)
	if err := c.sendMessage(msg); err != nil {
		c.setResponseHandler(c.mid, nil)
		return err
	}

	return nil
}

// Notify 发送通知到服务器
func (c *Connector) Notify(route string, v proto.Message) error {
	data, err := serialize(v)
	if err != nil {
		return err
	}

	msg := &message.Message{
		Type:  message.Notify,
		Route: route,
		Data:  data,
	}
	return c.sendMessage(msg)
}

// On 为事件添加回调
func (c *Connector) On(event string, callback Callback) {
	c.muEvents.Lock()
	defer c.muEvents.Unlock()

	c.events[event] = callback
}

// Close 关闭连接并关闭基准测试
func (c *Connector) Close() {
	c.conn.Close()
	close(c.die)
}

// eventHandler 获取事件处理器
func (c *Connector) eventHandler(event string) (Callback, bool) {
	c.muEvents.RLock()
	defer c.muEvents.RUnlock()

	cb, ok := c.events[event]
	return cb, ok
}

// responseHandler 获取响应处理器
func (c *Connector) responseHandler(mid uint64) (Callback, bool) {
	c.muResponses.RLock()
	defer c.muResponses.RUnlock()

	cb, ok := c.responses[mid]
	return cb, ok
}

// setResponseHandler 设置响应处理器
func (c *Connector) setResponseHandler(mid uint64, cb Callback) {
	c.muResponses.Lock()
	defer c.muResponses.Unlock()

	if cb == nil {
		delete(c.responses, mid)
	} else {
		c.responses[mid] = cb
	}
}

// sendMessage 发送消息
func (c *Connector) sendMessage(msg *message.Message) error {
	data, err := msg.Encode()
	if err != nil {
		return err
	}

	payload, err := codec.Encode(packet.Data, data)
	if err != nil {
		return err
	}

	c.mid++
	c.send(payload)

	return nil
}

// write 写入数据到连接
func (c *Connector) write() {
	defer close(c.chSend)

	for {
		select {
		case data := <-c.chSend:
			if _, err := c.conn.Write(data); err != nil {
				log.Println(err.Error())
				c.Close()
			}

		case <-c.die:
			return
		}
	}
}

// send 发送数据到发送队列
func (c *Connector) send(data []byte) {
	c.chSend <- data
}

// read 从连接读取数据
func (c *Connector) read() {
	buf := make([]byte, 2048)

	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			log.Println(err.Error())
			c.Close()
			return
		}

		packets, err := c.codec.Decode(buf[:n])
		if err != nil {
			log.Println(err.Error())
			c.Close()
			return
		}

		for i := range packets {
			p := packets[i]
			c.processPacket(p)
		}
	}
}

// processPacket 处理数据包
func (c *Connector) processPacket(p *packet.Packet) {
	switch p.Type {
	case packet.Handshake:
		c.send(had)
		c.connectedCallback()
	case packet.Data:
		msg, err := message.Decode(p.Data)
		if err != nil {
			log.Println(err.Error())
			return
		}
		c.processMessage(msg)

	case packet.Kick:
		c.Close()
	}
}

// processMessage 处理消息
func (c *Connector) processMessage(msg *message.Message) {
	switch msg.Type {
	case message.Push:
		cb, ok := c.eventHandler(msg.Route)
		if !ok {
			log.Println("未找到事件处理器", msg.Route)
			return
		}

		cb(msg.Data)

	case message.Response:
		cb, ok := c.responseHandler(msg.ID)
		if !ok {
			log.Println("未找到响应处理器", msg.ID)
			return
		}

		cb(msg.Data)
		c.setResponseHandler(msg.ID, nil)
	}
}

// serialize 序列化消息
func serialize(v proto.Message) ([]byte, error) {
	data, err := proto.Marshal(v)
	if err != nil {
		return nil, err
	}
	return data, nil
}
