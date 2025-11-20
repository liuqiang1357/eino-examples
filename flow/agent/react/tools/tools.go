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

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ToolExecutionState 用于在 InvokableRun 和 OnEnd callback 之间传递执行状态
// 使用指针类型，可以在 InvokableRun 中修改，在 OnEnd 中读取
type ToolExecutionState struct {
	// Success 表示工具调用是否成功
	Success bool
}

type toolStateKey struct{}

// GetToolState 从 context 中获取工具执行状态
// 如果不存在则返回 nil
func GetToolState(ctx context.Context) *ToolExecutionState {
	v := ctx.Value(toolStateKey{})
	if v == nil {
		return nil
	}
	state, ok := v.(*ToolExecutionState)
	if !ok {
		return nil
	}
	return state
}

// SetToolState 将工具执行状态设置到 context 中
// 返回新的 context（虽然返回的 context 不会被 InvokableRun 使用，
// 但指针指向的内存地址在 OnStart 和 OnEnd 中是共享的）
func SetToolState(ctx context.Context, state *ToolExecutionState) context.Context {
	return context.WithValue(ctx, toolStateKey{}, state)
}

// safeTool wraps a tool to convert errors into error messages that the model can handle.
// When a tool returns an error, safeTool returns the error message as a string instead of propagating the error,
// allowing the model to see the error and decide whether to retry or use another tool.
type safeTool struct {
	tool.InvokableTool
}

func (s safeTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return s.InvokableTool.Info(ctx)
}

func (s safeTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	out, e := s.InvokableTool.InvokableRun(ctx, argumentsInJSON, opts...)
	
	// 设置执行状态：仅当 e 为空时认为成功
	state := GetToolState(ctx)
	if state != nil {
		state.Success = (e == nil)
	}
	
	if e != nil {
		// Return error message as string instead of error, so the model can see it and decide next action
		return e.Error(), nil
	}
	return out, nil
}

func GetRestaurantTool() tool.InvokableTool {
	return safeTool{
		InvokableTool: &ToolQueryRestaurants{
			backService: restService,
		},
	}
}

func GetDishTool() tool.InvokableTool {
	return safeTool{
		InvokableTool: &ToolQueryDishes{
			backService: restService,
		},
	}
}

type ToolQueryRestaurants struct {
	backService *fakeService // fake service
}

func (t *ToolQueryRestaurants) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "query_restaurants",
		Desc: "Query restaurants",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"location": {
				Type:     "string",
				Desc:     "The location of the restaurant",
				Required: true,
			},
			"topn": {
				Type: "number",
				Desc: "top n restaurant in some location sorted by score",
			},
		}),
	}, nil
}

// InvokableRun
// tool 接收的参数和返回都是 string, 就如大模型的 tool call 的返回一样, 因此需要自行处理参数和结果的序列化.
// 返回的 content 会作为 schema.Message 的 content, 一般来说是作为大模型的输入, 因此处理成大模型能更好理解的结构最好.
// 因此，如果是 json 格式，就需要注意 key 和 value 的表意, 不要用 int Enum 代表一个业务含义，比如 `不要用 1 代表 male, 2 代表 female` 这类.
func (t *ToolQueryRestaurants) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// 解析参数
	p := &QueryRestaurantsParam{}
	err := json.Unmarshal([]byte(argumentsInJSON), p)
	if err != nil {
		return "", err
	}
	if p.Topn == 0 {
		p.Topn = 3
	}

	// 随机报错测试（50% 概率），错误中提示可以重试
	rand.Seed(time.Now().UnixNano())
	if rand.Float32() < 0.5 {
		errorMsg := map[string]string{
			"error":   "service temporarily unavailable",
			"message": "The restaurant service is temporarily unavailable. Please retry later.",
			"retry":   "true",
		}
		errorJSON, _ := json.Marshal(errorMsg)
		return "", fmt.Errorf("%s", string(errorJSON))
	}

	// 请求后端服务
	rests, err := t.backService.QueryRestaurants(ctx, p)
	if err != nil {
		return "", err
	}

	// 序列化结果
	res, err := json.Marshal(rests)
	if err != nil {
		return "", err
	}

	return string(res), nil
}

type QueryRestaurantsParam struct {
	Location string `json:"location"`
	Topn     int    `json:"topn"`
}

type Restaurant struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Place string `json:"place"`
	Desc  string `json:"desc"`
	Score int    `json:"score"`
}

// ToolQueryDishes.
type ToolQueryDishes struct {
	backService *fakeService // fake service
}

func (t *ToolQueryDishes) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "query_dishes",
		Desc: "查询一家餐厅有哪些菜品",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"restaurant_id": {
				Type:     "string",
				Desc:     "The id of one restaurant",
				Required: true,
			},
			"topn": {
				Type: "number",
				Desc: "top n dishes in one restaurant sorted by score",
			},
		}),
	}, nil
}

func (t *ToolQueryDishes) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// 解析参数
	p := &QueryDishesParam{}
	err := json.Unmarshal([]byte(argumentsInJSON), p)
	if err != nil {
		return "", err
	}

	if p.Topn == 0 {
		p.Topn = 5
	}

	// 请求后端服务
	rests, err := t.backService.QueryDishes(ctx, p)
	if err != nil {
		return "", err
	}

	// 序列化结果
	res, err := json.Marshal(rests)
	if err != nil {
		return "", err
	}

	return string(res), nil
}

type QueryDishesParam struct {
	RestaurantID string `json:"restaurant_id"`
	Topn         int    `json:"topn"`
}

type Dish struct {
	Name  string `json:"name"`
	Desc  string `json:"desc"`
	Price int    `json:"price"`
	Score int    `json:"score"`
}
