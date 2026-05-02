package mcp_server

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type CalculatorTool struct{}

func (t *CalculatorTool) Name() string        { return "calculator" }
func (t *CalculatorTool) Description() string { return "数学计算器，支持基本运算和常用数学函数" }
func (t *CalculatorTool) Type() int           { return 5 }
func (t *CalculatorTool) Parameters() []ToolParamDef {
	return []ToolParamDef{
		{Name: "expression", Type: "string", Description: "数学表达式，如 2+3*4 或 sqrt(16)", Required: true},
	}
}

func (t *CalculatorTool) Execute(ctx context.Context, params map[string]string) (*ToolResult, error) {
	expr := params["expression"]
	if expr == "" {
		return &ToolResult{Content: "缺少expression参数", IsError: true}, nil
	}

	result, err := evaluateExpression(expr)
	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("计算错误: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"expression": expr},
		}, nil
	}

	return &ToolResult{
		Content:  fmt.Sprintf("%s = %v", expr, result),
		Metadata: map[string]string{"expression": expr, "result": fmt.Sprintf("%v", result)},
	}, nil
}

func evaluateExpression(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)
	expr = strings.ReplaceAll(expr, " ", "")

	if strings.Contains(expr, "sqrt(") {
		return evalFunc(expr, "sqrt", math.Sqrt)
	}
	if strings.Contains(expr, "abs(") {
		return evalFunc(expr, "abs", math.Abs)
	}
	if strings.Contains(expr, "sin(") {
		return evalFunc(expr, "sin", func(f float64) float64 { return math.Sin(f * math.Pi / 180) })
	}
	if strings.Contains(expr, "cos(") {
		return evalFunc(expr, "cos", func(f float64) float64 { return math.Cos(f * math.Pi / 180) })
	}
	if strings.Contains(expr, "log(") {
		return evalFunc(expr, "log", math.Log10)
	}
	if strings.Contains(expr, "ln(") {
		return evalFunc(expr, "ln", math.Log)
	}
	if strings.Contains(expr, "pow(") {
		return evalPowFunc(expr)
	}

	return evalBasic(expr)
}

func evalFunc(expr, funcName string, fn func(float64) float64) (float64, error) {
	prefix := funcName + "("
	if !strings.HasPrefix(expr, prefix) || !strings.HasSuffix(expr, ")") {
		return 0, fmt.Errorf("invalid %s expression", funcName)
	}
	inner := expr[len(prefix) : len(expr)-1]
	val, err := strconv.ParseFloat(inner, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number in %s: %s", funcName, inner)
	}
	return fn(val), nil
}

func evalPowFunc(expr string) (float64, error) {
	if !strings.HasPrefix(expr, "pow(") || !strings.HasSuffix(expr, ")") {
		return 0, fmt.Errorf("invalid pow expression")
	}
	inner := expr[4 : len(expr)-1]
	parts := strings.Split(inner, ",")
	if len(parts) != 2 {
		return 0, fmt.Errorf("pow requires 2 arguments")
	}
	base, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid base: %s", parts[0])
	}
	exp, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid exponent: %s", parts[1])
	}
	return math.Pow(base, exp), nil
}

func evalBasic(expr string) (float64, error) {
	precedence := map[rune]int{'+': 1, '-': 1, '*': 2, '/': 2}

	var nums []float64
	var ops []rune

	applyOp := func() error {
		if len(nums) < 2 || len(ops) == 0 {
			return fmt.Errorf("invalid expression")
		}
		b := nums[len(nums)-1]
		a := nums[len(nums)-2]
		op := ops[len(ops)-1]
		nums = nums[:len(nums)-2]
		ops = ops[:len(ops)-1]

		var result float64
		switch op {
		case '+':
			result = a + b
		case '-':
			result = a - b
		case '*':
			result = a * b
		case '/':
			if b == 0 {
				return fmt.Errorf("division by zero")
			}
			result = a / b
		}
		nums = append(nums, result)
		return nil
	}

	i := 0
	for i < len(expr) {
		ch := rune(expr[i])
		if ch == '(' {
			ops = append(ops, ch)
			i++
		} else if ch == ')' {
			for len(ops) > 0 && ops[len(ops)-1] != '(' {
				if err := applyOp(); err != nil {
					return 0, err
				}
			}
			if len(ops) == 0 {
				return 0, fmt.Errorf("mismatched parentheses")
			}
			ops = ops[:len(ops)-1]
			i++
		} else if ch == '+' || ch == '-' || ch == '*' || ch == '/' {
			if ch == '-' && (i == 0 || rune(expr[i-1]) == '(') {
				j := i + 1
				for j < len(expr) && (rune(expr[j]) >= '0' && rune(expr[j]) <= '9' || rune(expr[j]) == '.') {
					j++
				}
				num, err := strconv.ParseFloat(expr[i:j], 64)
				if err != nil {
					return 0, fmt.Errorf("invalid number: %s", expr[i:j])
				}
				nums = append(nums, num)
				i = j
				continue
			}
			for len(ops) > 0 && ops[len(ops)-1] != '(' && precedence[ops[len(ops)-1]] >= precedence[ch] {
				if err := applyOp(); err != nil {
					return 0, err
				}
			}
			ops = append(ops, ch)
			i++
		} else if (ch >= '0' && ch <= '9') || ch == '.' {
			j := i
			for j < len(expr) && (rune(expr[j]) >= '0' && rune(expr[j]) <= '9' || rune(expr[j]) == '.') {
				j++
			}
			num, err := strconv.ParseFloat(expr[i:j], 64)
			if err != nil {
				return 0, fmt.Errorf("invalid number: %s", expr[i:j])
			}
			nums = append(nums, num)
			i = j
		} else {
			return 0, fmt.Errorf("unexpected character: %c", ch)
		}
	}

	for len(ops) > 0 {
		if ops[len(ops)-1] == '(' {
			return 0, fmt.Errorf("mismatched parentheses")
		}
		if err := applyOp(); err != nil {
			return 0, err
		}
	}

	if len(nums) != 1 {
		return 0, fmt.Errorf("invalid expression")
	}

	return nums[0], nil
}
