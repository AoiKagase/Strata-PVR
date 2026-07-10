package scheduler

import (
	"sort"
	"strconv"
	"strings"

	"strata-pvr/internal/config"
	legacy "strata-pvr/internal/domain"
	"strata-pvr/internal/mirakurun"
)

var genreTable = map[int]string{
	0x0: "news",
	0x1: "sports",
	0x2: "information",
	0x3: "drama",
	0x4: "music",
	0x5: "variety",
	0x6: "cinema",
	0x7: "anime",
	0x8: "documentary",
	0x9: "theater",
	0xA: "hobby",
	0xB: "welfare",
	0xC: "etc",
	0xD: "etc",
	0xE: "etc",
	0xF: "etc",
}

func BuildSchedule(cfg *config.Config, services []mirakurun.Service, programs []mirakurun.Program) []legacy.ChannelSchedule {
	services = filterAndOrderServices(cfg, append([]mirakurun.Service(nil), services...))
	channels := make([]legacy.ChannelSchedule, 0, len(services))
	for i, service := range services {
		channel := legacy.ChannelSchedule{
			Channel: legacy.Channel{
				Type:        service.Channel.Type,
				Channel:     service.Channel.Channel,
				Name:        service.Name,
				ID:          strconv.FormatInt(service.ID, 36),
				SID:         service.ServiceID,
				NID:         service.NetworkID,
				N:           i,
				HasLogoData: service.HasLogoData,
			},
		}
		channel.Programs = convertPrograms(channel.Channel, programs)
		channels = append(channels, channel)
	}
	sort.SliceStable(channels, func(i, j int) bool {
		if channels[i].N == channels[j].N {
			return channels[i].SID < channels[j].SID
		}
		return channels[i].N < channels[j].N
	})
	return channels
}

func filterAndOrderServices(cfg *config.Config, services []mirakurun.Service) []mirakurun.Service {
	excluded := map[int64]bool{}
	for _, id := range cfg.ExcludeServices {
		excluded[id] = true
	}
	filtered := services[:0]
	for _, service := range services {
		if !excluded[service.ID] {
			filtered = append(filtered, service)
		}
	}
	insert := 0
	for _, id := range cfg.ServiceOrder {
		idx := -1
		for i := range filtered {
			if filtered[i].ID == id {
				idx = i
				break
			}
		}
		if idx == -1 {
			continue
		}
		if idx == insert {
			insert++
			continue
		}
		service := filtered[idx]
		filtered = append(filtered[:idx], filtered[idx+1:]...)
		head := append([]mirakurun.Service{}, filtered[:insert]...)
		head = append(head, service)
		filtered = append(head, filtered[insert:]...)
		insert++
	}
	return filtered
}

func convertPrograms(channel legacy.Channel, programs []mirakurun.Program) []legacy.Program {
	out := []legacy.Program{}
	for _, program := range programs {
		if program.NetworkID != channel.NID || program.ServiceID != channel.SID {
			continue
		}
		category := "etc"
		if len(program.Genres) > 0 {
			if v, ok := genreTable[program.Genres[0].Lv1]; ok {
				category = v
			}
		}
		detail := program.Description
		description := ""
		if len(program.Extended) > 0 {
			description = program.Description
			for key, value := range program.Extended {
				detail += "\n◇" + key + "\n" + value
			}
			detail = strings.TrimSpace(detail)
		}
		p := legacy.Program{
			ID:          strconv.FormatInt(program.ID, 36),
			Category:    category,
			Title:       stripProgramFlags(program.Name),
			FullTitle:   program.Name,
			Detail:      detail,
			Description: description,
			Start:       program.StartAt,
			End:         program.StartAt + program.Duration,
			Seconds:     program.Duration / 1000,
			Flags:       extractFlags(program.Name),
			Channel:     channel,
		}
		out = append(out, p)
	}
	return out
}

func stripProgramFlags(title string) string {
	replacer := strings.NewReplacer("[無料]", "", "[生放送]", "", "[新]", "", "[終]", "", "[再]", "", "[字]", "", "[デ]", "", "[解]", "", "[無]", "", "[二]", "", "[S]", "", "[SS]", "", "[初]", "", "[生]", "", "[Ｎ]", "", "[映]", "", "[多]", "", "[双]", "")
	return strings.TrimSpace(replacer.Replace(title))
}

func extractFlags(title string) []string {
	replacements := map[string]string{"[無料]": "無", "[生放送]": "生"}
	for from, to := range replacements {
		title = strings.ReplaceAll(title, from, "["+to+"]")
	}
	known := []string{"新", "終", "再", "字", "デ", "解", "無", "二", "S", "SS", "初", "生", "Ｎ", "映", "多", "双"}
	flags := []string{}
	for _, flag := range known {
		if strings.Contains(title, "["+flag+"]") || strings.Contains(title, "【"+flag+"】") || strings.Contains(title, "("+flag+")") {
			flags = append(flags, flag)
		}
	}
	return flags
}
