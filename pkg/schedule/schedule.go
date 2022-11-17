package schedulecheck

import (
	"time"

	"github.com/go-co-op/gocron"
	"github.com/go-logr/logr"
	"github.com/pion/ion-sfu/cmd/signal/json-rpc/server"
	cacheredis "github.com/pion/ion-sfu/pkg/cache"
	"github.com/pion/ion-sfu/pkg/sfu"
)

func ScheduleCheckSession(s *sfu.SFU, logger logr.Logger) {

	cron := gocron.NewScheduler(time.UTC)
	cron.Every("5m").Do(func() {
		logger.Info("Schedule check session...")
		var deleteSid []string
		ssids, errGet := cacheredis.GetCacheRedis("sessionid")
		if errGet != nil {
			logger.Error(errGet, "Err get redis")
		}
		for id, _ := range server.PullPeers {
			check := false
			for _, sid := range ssids {
				if id == sid {
					check = true
					break
				}
			}
			if check == false {
				deleteSid = append(deleteSid, id)
			}
		}
		for _, id := range deleteSid {
			delete(server.PullPeers, id)
		}
	})
	cron.StartAsync()
}
