package cli

import (
	"errors"

	"github.com/gatework/go-wireless/internal/bootstrap"
)

func setupMessage(err error) string {
	switch {
	case errors.Is(err, bootstrap.ErrCredentials):
		return "请设置 NC_DEVICE_USERNAME 或平台 NC_*_USERNAME，并检查设备连接参数"
	case errors.Is(err, bootstrap.ErrPlatform):
		return "WIRELESS 平台错误"
	case errors.Is(err, bootstrap.ErrConnection):
		return "WIRELESS 连接参数错误"
	case errors.Is(err, bootstrap.ErrResources):
		return "本机文件描述符资源不足；请降低 --parallel 或调整 ulimit -n"
	case errors.Is(err, bootstrap.ErrReport):
		return "WIRELESS 报告目录须为真实私有目录0700，且可写"
	case errors.Is(err, bootstrap.ErrEnvironment):
		return "凭据环境加载失败"
	default:
		return "WIRELESS 配置或依赖错误"
	}
}
