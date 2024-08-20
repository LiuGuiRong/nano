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
	// 定义一个错误变量，用于存储编码过程中可能发生的错误
	var err error

	// 对握手数据进行编码，并将结果存储在全局变量 hsd 中
	// 如果编码过程中发生错误，则引发恐慌，终止程序
	hsd, err = codec.Encode(packet.Handshake, nil)
	if err != nil {
		panic(err)
	}

	// 对握手确认数据进行编码，并将结果存储在全局变量 had 中
	// 如果编码过程中发生错误，则引发恐慌，终止程序
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
