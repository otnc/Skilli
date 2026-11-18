package cleanup

import (
	"errors"
	"strings"
)

// Describe explains in Japanese why a cleanup was refused or failed and
// what the user can do, for the GUI and the CLI to show alike. The
// technical message follows on its own line.
//
//	fmt.Println(cleanup.Describe(err))
func Describe(err error) string {
	var reason string
	switch {
	case errors.Is(err, ErrInUse):
		reason = "このキャッシュを使っているプログラムが動いているため、中止しました。次のプログラムを終了してからやり直してください。常駐しているサーバー (uv run で起動した MCP サーバーなど) も対象です。"
	case errors.Is(err, ErrWrongVersion):
		reason = "ホームフォルダで実行した yarn などのバージョンがこの対象に合わないため、別のキャッシュを消さないよう中止しました。"
	case errors.Is(err, ErrBusy):
		reason = "ほかの掃除が実行中です (CLI を含む)。終わってからやり直してください。"
	case errors.Is(err, ErrPlanExpired):
		reason = "確認してから時間がたちすぎたため、中止しました。もう一度やり直してください。"
	case errors.Is(err, ErrPlanChanged):
		reason = "確認したあとにツールかフォルダが変わったため、中止しました。もう一度やり直してください。"
	case errors.Is(err, ErrUnsafeFolder):
		reason = "保護しているフォルダか、リンクを経由するフォルダのため、掃除しません。"
	case strings.Contains(err.Error(), "did not finish within"):
		reason = "時間内に終わらなかったため、中止しました。"
	default:
		return err.Error()
	}
	return reason + "\n" + err.Error()
}
