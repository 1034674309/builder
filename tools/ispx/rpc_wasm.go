//go:build js && wasm

package main

import (
	"fmt"
	"log"
	"sync"
	"syscall/js"

	"github.com/goplus/builder/tools/ai"
	"github.com/goplus/builder/tools/ispx/internal/rpc"
	"github.com/goplus/xgo/x/jsonrpc2"
)

var rpcSession struct {
	sync.RWMutex
	client *rpc.Client
}

func init() {
	js.Global().Set("xbuilder_set_message_replier", js.FuncOf(setMessageReplier))
	js.Global().Set("xbuilder_handle_rpc_message", js.FuncOf(handleRPCMessage))
	ai.SetDefaultTracer(nil)
}

type messageReplier struct {
	value js.Value
}

func (r *messageReplier) SendMessage(message jsonrpc2.Message) (err error) {
	raw, err := jsonrpc2.EncodeMessage(message)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if jsErr, ok := recovered.(js.Error); ok {
				err = fmt.Errorf("message replier: %w", jsErr)
			} else {
				err = fmt.Errorf("message replier panic: %v", recovered)
			}
		}
	}()
	value := js.Global().Get("JSON").Call("parse", string(raw))
	r.value.Invoke(value)
	return nil
}

func setMessageReplier(this js.Value, args []js.Value) any {
	var next *rpc.Client
	if len(args) > 0 && args[0].Type() == js.TypeFunction {
		next = rpc.NewClient(&messageReplier{value: args[0]})
	}

	rpcSession.Lock()
	previous := rpcSession.client
	rpcSession.client = next
	rpcSession.Unlock()
	if previous != nil {
		previous.Close()
	}
	if next == nil {
		ai.SetDefaultTracer(nil)
	} else {
		ai.SetDefaultTracer(NewRPCTracer(next))
	}
	return nil
}

func handleRPCMessage(this js.Value, args []js.Value) any {
	if len(args) == 0 {
		return nil
	}
	if err := receiveRPCMessage(args[0]); err != nil {
		log.Printf("failed to handle RPC message: %v", err)
	}
	return nil
}

func receiveRPCMessage(value js.Value) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if jsErr, ok := recovered.(js.Error); ok {
				err = fmt.Errorf("read RPC message: %w", jsErr)
			} else {
				err = fmt.Errorf("read RPC message panic: %v", recovered)
			}
		}
	}()

	raw := value.String()
	if value.Type() != js.TypeString {
		raw = js.Global().Get("JSON").Call("stringify", value).String()
	}
	message, err := jsonrpc2.DecodeMessage([]byte(raw))
	if err != nil {
		return fmt.Errorf("decode RPC message: %w", err)
	}
	rpcSession.RLock()
	client := rpcSession.client
	rpcSession.RUnlock()
	if client == nil {
		return nil
	}
	return client.HandleMessage(message)
}
