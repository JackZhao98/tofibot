package executor

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// CheckShellSafety 对脚本进行静态分析，检查潜在的安全风险
// 主要检查：变量引用是否被双引号包裹
func CheckShellSafety(script string) error {
	in := strings.NewReader(script)
	parser := syntax.NewParser()

	// 解析脚本为 AST
	f, err := parser.Parse(in, "")
	if err != nil {
		return fmt.Errorf("shell 语法错误: %v", err)
	}

	checker := &safetyChecker{}
	checker.visit(f, false)

	if len(checker.errors) > 0 {
		return fmt.Errorf("脚本安全检查失败:\n%s", strings.Join(checker.errors, "\n"))
	}

	return nil
}

type safetyChecker struct {
	errors []string
}

func (c *safetyChecker) visit(node syntax.Node, inDoubleQuote bool) {
	if node == nil {
		return
	}

	switch x := node.(type) {
	case *syntax.File:
		for _, stmt := range x.Stmts {
			c.visit(stmt, inDoubleQuote)
		}
	case *syntax.Stmt:
		c.visit(x.Cmd, inDoubleQuote)
	case *syntax.CallExpr:
		for _, arg := range x.Args {
			c.visit(arg, inDoubleQuote)
		}
	case *syntax.Word:
		for _, part := range x.Parts {
			c.visit(part, inDoubleQuote)
		}
	case *syntax.DblQuoted:
		// 进入双引号区域，标记为安全
		for _, part := range x.Parts {
			c.visit(part, true)
		}
	case *syntax.ParamExp:
		// 如果当前不在双引号内，报错
		if !inDoubleQuote {
			name := x.Param.Value
			if name == "" { // 处理 $1, $2 等
				name = "?"
			}
			msg := fmt.Sprintf("  - Line %d: 变量 $%s 未加双引号 (建议改为 \"$%s\")", x.Pos().Line(), name, name)
			c.errors = append(c.errors, msg)
		}

	case *syntax.CmdSubst:
		for _, stmt := range x.Stmts {
			c.visit(stmt, false) // 命令替换 $(...) 内部重置为非引用状态
		}
	case *syntax.Subshell:
		for _, stmt := range x.Stmts {
			c.visit(stmt, inDoubleQuote)
		}
	case *syntax.Block:
		for _, stmt := range x.Stmts {
			c.visit(stmt, inDoubleQuote)
		}
	case *syntax.IfClause:
		c.visitStmts(x.Cond, inDoubleQuote)
		c.visitStmts(x.Then, inDoubleQuote)
		if x.Else != nil {
			c.visit(x.Else, inDoubleQuote)
		}

	case *syntax.BinaryCmd:
		c.visit(x.X, inDoubleQuote)
		c.visit(x.Y, inDoubleQuote)
	case *syntax.FuncDecl:
		c.visit(x.Body, inDoubleQuote)
	case *syntax.ForClause:
		c.visitStmts(x.Do, inDoubleQuote)
	case *syntax.WhileClause:
		c.visitStmts(x.Do, inDoubleQuote)
	case *syntax.CaseClause:
		for _, item := range x.Items {
			c.visitStmts(item.Stmts, inDoubleQuote)
		}
	}
}

// 辅助函数：遍历语句列表
func (c *safetyChecker) visitStmts(stmts []*syntax.Stmt, inDoubleQuote bool) {
	for _, stmt := range stmts {
		c.visit(stmt, inDoubleQuote)
	}
}
