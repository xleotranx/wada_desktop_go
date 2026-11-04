package cowork

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"strconv"
)

func calculate(expression string) (string, error) {
	if len(expression) == 0 || len(expression) > 4096 {
		return "", errors.New("provide an arithmetic expression up to 4096 characters")
	}
	tree, err := parser.ParseExpr(expression)
	if err != nil {
		return "", errors.New("use decimal numbers, parentheses and + - * / % operators")
	}
	var evaluate func(ast.Expr, int) (float64, error)
	evaluate = func(node ast.Expr, depth int) (float64, error) {
		if depth > 64 {
			return 0, errors.New("expression is too deeply nested")
		}
		switch n := node.(type) {
		case *ast.BasicLit:
			if n.Kind != token.INT && n.Kind != token.FLOAT {
				return 0, errors.New("only decimal numbers are supported")
			}
			return strconv.ParseFloat(n.Value, 64)
		case *ast.ParenExpr:
			return evaluate(n.X, depth+1)
		case *ast.UnaryExpr:
			value, err := evaluate(n.X, depth+1)
			if err != nil {
				return 0, err
			}
			switch n.Op.String() {
			case "+":
				return value, nil
			case "-":
				return -value, nil
			default:
				return 0, errors.New("unsupported unary operator")
			}
		case *ast.BinaryExpr:
			left, err := evaluate(n.X, depth+1)
			if err != nil {
				return 0, err
			}
			right, err := evaluate(n.Y, depth+1)
			if err != nil {
				return 0, err
			}
			switch n.Op.String() {
			case "+":
				return left + right, nil
			case "-":
				return left - right, nil
			case "*":
				return left * right, nil
			case "/":
				if right == 0 {
					return 0, errors.New("division by zero")
				}
				return left / right, nil
			case "%":
				if right == 0 {
					return 0, errors.New("division by zero")
				}
				return math.Mod(left, right), nil
			default:
				return 0, errors.New("unsupported arithmetic operator")
			}
		default:
			return 0, fmt.Errorf("only numeric arithmetic is supported; no names, functions or code")
		}
	}
	value, err := evaluate(tree, 0)
	if err != nil {
		return "", err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", errors.New("calculation exceeds the supported numeric range")
	}
	data, err := json.Marshal(map[string]any{"expression": expression, "value": value, "precision": "IEEE-754 floating point; apply explicit rounding for currency presentation"})
	return string(data), err
}
