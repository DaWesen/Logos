package cmd

import (
	"strings"

	zero "github.com/wdvxdr1123/ZeroBot"
)

// 模块接口，所有功能模块都需要实现此接口
type Module interface {
	//返回模块名称
	Name() string

	//处理模块内的命令
	//返回true表示命令已处理，false表示命令未处理
	HandleCommand(cmd string, args []string, ctx *zero.Ctx) bool

	//进入模块时执行的操作
	Enter(ctx *zero.Ctx)

	//退出模块时执行的操作
	Exit(ctx *zero.Ctx)
}

// 命令系统，负责管理模块和处理用户输入
type CommandSystem struct {
	currentModule  string                         //当前激活的模块
	modules        map[string]Module              //注册的功能模块
	directCommands map[string]func(ctx *zero.Ctx) //直接执行的命令
}

// 创建新的命令系统实例
func NewCommandSystem() *CommandSystem {
	return &CommandSystem{
		currentModule:  "",
		modules:        make(map[string]Module),
		directCommands: make(map[string]func(ctx *zero.Ctx)),
	}
}

// 注册功能模块
func (cs *CommandSystem) RegisterModule(module Module) {
	cs.modules[module.Name()] = module
}

// 注册直接执行的命令
func (cs *CommandSystem) RegisterDirectCommand(cmd string, handler func(ctx *zero.Ctx)) {
	cs.directCommands[cmd] = handler
}

// 处理用户输入
func (cs *CommandSystem) ProcessInput(input string, ctx *zero.Ctx) bool {
	//检查是否是命令（以/开头）
	if strings.HasPrefix(input, "/") {
		cmd := strings.TrimPrefix(input, "/")

		//检查是否是直接执行的命令
		if handler, exists := cs.directCommands[cmd]; exists {
			handler(ctx)
			return true
		}

		//检查是否是退出命令
		if cmd == "exit" {
			if cs.currentModule != "" {
				cs.modules[cs.currentModule].Exit(ctx)
				cs.currentModule = ""
				ctx.Send("已退出当前功能系统")
			} else {
				ctx.Send("当前不在任何功能系统中")
			}
			return true
		}

		//检查是否是帮助命令
		if cmd == "help" {
			cs.showHelp(ctx)
			return true
		}

		//检查是否是有效的模块名称
		if module, exists := cs.modules[cmd]; exists {
			//退出当前模块
			if cs.currentModule != "" {
				cs.modules[cs.currentModule].Exit(ctx)
			}

			//进入新模块
			cs.currentModule = cmd
			module.Enter(ctx)
			return true
		} else {
			//不是系统命令，让plugin处理
			//移除/前缀，让plugin处理
			ctx.Event.RawMessage = cmd
			//让ZeroBot继续处理这个消息
			return false
		}
	}

	//在当前模块中处理命令
	if cs.currentModule != "" {
		module := cs.modules[cs.currentModule]

		//解析输入为命令和参数
		parts := strings.Fields(input)
		if len(parts) > 0 {
			cmd := parts[0]
			args := parts[1:]

			//处理命令
			handled := module.HandleCommand(cmd, args, ctx)
			if !handled {
				ctx.Send("未知的命令，请输入 /help 查看帮助")
			}
		} else {
			ctx.Send("请输入命令，或输入 /exit 退出当前功能系统")
		}
		return true
	}

	// 未处理的输入
	return false
}

// 显示帮助信息
func (cs *CommandSystem) showHelp(ctx *zero.Ctx) {
	helpMsg := "可用功能系统:\n"
	for name := range cs.modules {
		helpMsg += "- /" + name + "\n"
	}
	helpMsg += "\n可用直接命令:\n"
	for cmd := range cs.directCommands {
		helpMsg += "- /" + cmd + "\n"
	}
	helpMsg += "\n当前状态: "
	if cs.currentModule != "" {
		helpMsg += "已进入 " + cs.currentModule + " 系统\n"
		helpMsg += "输入 /exit 退出当前系统"
	} else {
		helpMsg += "未进入任何功能系统"
	}
	ctx.Send(helpMsg)
}
