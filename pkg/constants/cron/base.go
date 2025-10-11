package cron

import "github.com/robfig/cron/v3"

const CronParserOptions = cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor // nolint:lll
