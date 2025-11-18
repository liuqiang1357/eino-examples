/*
 * Copyright 2024 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cloudwego/eino-examples/flow/agent/react/tools"
	"github.com/cloudwego/eino-ext/components/model/deepseek"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

func main() {
	ctx := context.Background()

	config := &deepseek.ChatModelConfig{
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		Model:   "deepseek-chat",
	}

	arkModel, err := deepseek.NewChatModel(ctx, config)
	if err != nil {
		fmt.Printf("[ERROR] failed to create chat model: %v\n", err)
		return
	}

	toolCallChecker := func(ctx context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
		defer sr.Close()
		for {
			msg, err := sr.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return false, nil
				}
				return false, err
			}
			if len(msg.ToolCalls) > 0 {
				return true, nil
			}
		}
	}

	ragent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel:      arkModel,
		StreamToolCallChecker: toolCallChecker,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: []tool.BaseTool{
				tools.GetRestaurantTool(),
				tools.GetDishTool(),
			},
		},
	})
	if err != nil {
		fmt.Printf("[ERROR] failed to create agent: %v\n", err)
		return
	}

	sr, err := ragent.Stream(ctx, []*schema.Message{
		{
			Role:    schema.System,
			Content: `# Character:
你是一个帮助用户推荐餐厅和菜品的助手，根据用户的需要，查询餐厅信息并推荐，查询餐厅的菜品并推荐。
`,
		},
		{
			Role:    schema.User,
			Content: "我在北京，给我推荐一些菜，需要有口味辣一点的菜，至少推荐有 2 家餐厅",
		},
	}, agent.WithComposeOptions(compose.WithCallbacks(&LoggerCallback{})))
	if err != nil {
		fmt.Printf("[ERROR] failed to stream: %v\n", err)
		return
	}

	defer sr.Close()

	fmt.Printf("[STREAM] Start streaming...\n\n")

	// Drain the stream to ensure all callbacks are executed and the stream completes
	for {
		_, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			fmt.Printf("[ERROR] failed to recv: %v\n", err)
			return
		}
	}

	fmt.Printf("\n[STREAM] Finished\n")
}

type LoggerCallback struct {
	callbacks.HandlerBuilder
}

func (cb *LoggerCallback) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	if info.Component == components.ComponentOfTool {
		tci := tool.ConvCallbackInput(input)
		if tci != nil {
			fmt.Printf("[TOOL] %s: %s\n", info.Name, tci.ArgumentsInJSON)
		}
	}
	return ctx
}

func (cb *LoggerCallback) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	if info.Component == components.ComponentOfTool {
		tco := tool.ConvCallbackOutput(output)
		if tco != nil {
			responseStr := tco.Response
			if len(responseStr) > 200 {
				responseStr = responseStr[:200] + "..."
			}
			fmt.Printf("[TOOL] %s: result = %s\n", info.Name, responseStr)
		}
	}
	return ctx
}

func (cb *LoggerCallback) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	fmt.Printf("[ERROR] [%s:%s:%s] %v\n", info.Component, info.Type, info.Name, err)
	return ctx
}

func (cb *LoggerCallback) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo,
	input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	defer input.Close()
	return ctx
}

func (cb *LoggerCallback) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo,
	output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	// Only handle ChatModel stream output to avoid blocking by toolCallChecker
	if info.Component == components.ComponentOfChatModel {
		go func() {
			defer output.Close()
			for {
				frame, err := output.Recv()
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					fmt.Printf("[ERROR] failed to recv from stream: %v\n", err)
					return
				}

				// Handle different types of output
				switch v := frame.(type) {
				case *schema.Message:
					if v.Content != "" {
						fmt.Printf("%v: %v\n", schema.Assistant, v.Content)
					}
				case *model.CallbackOutput:
					if v.Message != nil && v.Message.Content != "" {
						fmt.Printf("%v: %v\n", schema.Assistant, v.Message.Content)
					}
				}
			}
		}()
	} else {
		defer output.Close()
	}
	return ctx
}
