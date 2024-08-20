package io

import (
	"log"         // 导入日志包，用于记录日志
	"os"          // 导入操作系统包，用于与操作系统交互
	"os/signal"   // 导入信号包，用于处理操作系统信号
	"sync/atomic" // 导入原子操作包，用于原子操作
	"syscall"     // 导入系统调用包，用于系统调用
	"testing"     // 导入测试包，用于编写测试
	"time"        // 导入时间包，用于时间相关操作

	"github.com/lonng/nano"                    // 导入 nano 包，用于创建服务器和客户端
	"github.com/lonng/nano/benchmark/testdata" // 导入 nano 的测试数据包
	"github.com/lonng/nano/component"          // 导入 nano 的组件包
	"github.com/lonng/nano/serialize/protobuf" // 导入 nano 的 Protobuf 序列化包
	"github.com/lonng/nano/session"            // 导入 nano 的会话包
)

const (
	addr = "127.0.0.1:13250" // 本地地址，用于服务器监听
	conc = 1000              // 并发客户端数量
)

type TestHandler struct {
	component.Base             // 嵌入 nano 的基础组件
	metrics        int32       // 用于记录 QPS（每秒请求数）的计数器
	group          *nano.Group // 用于管理会话的组
}

func (h *TestHandler) AfterInit() {
	ticker := time.NewTicker(time.Second) // 创建一个每秒触发一次的定时器

	// 定时输出 QPS（每秒请求数）
	go func() {
		for range ticker.C {
			println("QPS", atomic.LoadInt32(&h.metrics)) // 输出当前 QPS
			atomic.StoreInt32(&h.metrics, 0)             // 重置 QPS 计数器
		}
	}()
}

func NewTestHandler() *TestHandler {
	return &TestHandler{
		group: nano.NewGroup("handler"), // 创建一个新的组
	}
}

func (h *TestHandler) Ping(s *session.Session, data *testdata.Ping) error {
	atomic.AddInt32(&h.metrics, 1)                               // 增加 QPS 计数器
	return s.Push("pong", &testdata.Pong{Content: data.Content}) // 返回 Pong 响应
}

func server() {
	components := &component.Components{} // 创建组件容器
	components.Register(NewTestHandler()) // 注册 TestHandler 组件

	nano.Listen(addr,
		nano.WithDebugMode(),                          // 启用调试模式
		nano.WithSerializer(protobuf.NewSerializer()), // 使用 Protobuf 序列化器
		nano.WithComponents(components),               // 注册组件
	)
}

func client() {
	c := NewConnector() // 创建新的连接器

	chReady := make(chan struct{}) // 创建一个通道，用于通知连接成功
	c.OnConnected(func() {
		chReady <- struct{}{} // 连接成功时发送通知
	})

	if err := c.Start(addr); err != nil { // 启动客户端并连接服务器
		panic(err) // 如果连接失败，抛出异常
	}

	c.On("pong", func(data interface{}) {}) // 注册处理 Pong 响应的回调函数

	<-chReady // 等待连接成功通知
	for {
		c.Notify("TestHandler.Ping", &testdata.Ping{}) // 发送 Ping 请求
		time.Sleep(10 * time.Millisecond)              // 每 10 毫秒发送一次
	}
}

func TestIO(t *testing.T) {
	go server() // 启动服务器

	// 等待服务器启动
	time.Sleep(1 * time.Second)
	for i := 0; i < conc; i++ {
		go client() // 启动多个客户端
	}

	log.SetFlags(log.LstdFlags | log.Llongfile) // 设置日志标志

	sg := make(chan os.Signal)                                          // 创建一个通道，用于接收系统信号
	signal.Notify(sg, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGKILL) // 监听系统信号

	<-sg // 等待接收信号

	t.Log("exit") // 记录退出日志
}
