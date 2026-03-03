package interfaces

// StartCmdHandler 定义了处理 /start 命令的接口
type StartCmdHandler interface {
	HandleStartCmd(chatID int64, botIDs string, ownerID int64) (string, error)
}
