package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo"
)

const updateNotificationID = "skilli-update-available"

func watchUpdates(ctx context.Context) {
	if mygo.IsDev() {
		return
	}

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	notifiedVersions := map[string]struct{}{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		update, err := findUpdate(ctx)
		if errors.Is(err, mygo.ErrUpdatesDisabled) || ctx.Err() != nil {
			return
		}
		if err == nil && update != nil {
			if _, shown := notifiedVersions[update.Version]; !shown {
				if notifyUpdate(ctx, update.Version) == nil {
					notifiedVersions[update.Version] = struct{}{}
				}
			}
		}
		timer.Reset(6 * time.Hour)
	}
}

func findUpdate(ctx context.Context) (*mygo.Update, error) {
	checkContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return mygo.Updater.Check(checkContext)
}

func notifyUpdate(ctx context.Context, version string) error {
	noticeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	notice := mygo.NewNotification(mygo.NotificationOptions{
		ID:    updateNotificationID,
		Title: "Skilli の更新",
		Body:  "バージョン " + version + " を利用できます。クリックすると更新を確認できます。",
	})
	return notice.ShowContext(noticeContext)
}

func (d *dashboard) checkUpdates() {
	d.show()
	if d.updating {
		return
	}
	d.updating = true
	d.window.Invalidate()
	go func() {
		defer mygo.RunOnMain(func() {
			d.updating = false
			d.installing = false
			if d.window != nil {
				d.window.Invalidate()
			}
		})

		update, err := findUpdate(context.Background())
		switch {
		case errors.Is(err, mygo.ErrUpdatesDisabled):
			updateMessage("このビルドではアプリ内更新を利用できません。", "GitHub Releases の配布版から利用できます。")
			return
		case err != nil:
			updateMessage("更新を確認できませんでした。", err.Error())
			return
		case update == nil:
			updateMessage("Skilli は最新です。", "")
			return
		}

		answer, err := mygo.Dialog.Message(mygo.MessageOptions{
			Title:         "Skilli の更新",
			Message:       "バージョン " + update.Version + " を利用できます。",
			Detail:        "更新すると Skilli が再起動します。",
			Buttons:       []string{"あとで", "更新して再起動"},
			CancelButton:  0,
			DefaultButton: 0,
		})
		if err != nil || answer.Button != 1 {
			return
		}

		mygo.RunOnMain(func() {
			d.installing = true
			if d.window != nil {
				d.window.Invalidate()
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := update.Install(ctx, nil); err != nil {
			updateMessage("Skilli を更新できませんでした。", err.Error())
			return
		}
		mygo.RunOnMain(func() {
			d.installing = false
			mygo.App.Relaunch("--open")
		})
	}()
}

func updateMessage(message, detail string) {
	_, _ = mygo.Dialog.Message(mygo.MessageOptions{
		Title:   "Skilli の更新",
		Message: message,
		Detail:  detail,
	})
}
