package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/redis/go-redis/v9"
	"gopkg.in/telebot.v3"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	RedisAdmins       = "admins"
	RedisSources      = "src"
	RedisDestinations = "dst"
	RedisChatsAdded   = "allowed"

	TmpMsgLifetime = time.Second * 10
)

type Config struct {
	Token        string `json:"token"`
	RedisAddress string `json:"redis-address"`
	RedisDBId    int    `json:"redis-db-id"`
	RedisPrefix  string `json:"redis-prefix"`
}

func ReadConfig(path string) (*Config, error) {
	buffer, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config Config
	err = json.Unmarshal(buffer, &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

type Reposter struct {
	Bot     *telebot.Bot
	DB      *redis.Client
	Context context.Context
}

func ReplyTemporary(lifetime time.Duration, c telebot.Context, what any, opts ...any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("recover after %v", r)
			err = errors.New(fmt.Sprintf("recover after %v", r))
		}
	}()

	msg, err := c.Bot().Reply(c.Message(), what, opts...)
	if err != nil {
		return
	}

	go time.AfterFunc(lifetime, func() {
		_ = c.Bot().Delete(msg)
	})

	return
}

func Ok(c telebot.Context, err error) error {
	if err != nil {
		return err
	}
	return ReplyTemporary(TmpMsgLifetime, c, "+")
}

func NewReposter(bot *telebot.Bot, db *redis.Client, ctx context.Context, cfg *Config) *Reposter {
	bot.Handle("/start", func(c telebot.Context) error {
		admins, err := SMembersInt64(ctx, cfg, db, RedisAdmins)
		if err != nil {
			log.Printf("while /setup, %s", err.Error())
			return err
		}
		isAdmin, err := Contains(ctx, cfg, db, RedisAdmins, c.Chat().ID)
		if err != nil {
			return err
		}
		if len(admins) == 0 || isAdmin {
			return c.Reply("Hiii, this bot is made for reposting your lovely channels to your comfy chats\n" +
				"\n" +
				"/info  -- get admins/channels/chats list\n" +
				"/setup -- if you just started use this command\n" +
				"/ping  -- check if the bot is online \n" +
				"/clear -- remove all admins/channels/chats\n" +
				"\n" +
				"/add_admin, /add_chan, /add_chat <...> -- example: /add_chan 123 -456 -780\n" +
				"/del_admin, /del_chan, /del_chat <...> -- remove some admins/channels/chats")
		}
		return nil
	})
	bot.Handle("/add_chan", func(c telebot.Context) error {
		isAdmin, err := Contains(ctx, cfg, db, RedisAdmins, c.Chat().ID)
		if err != nil {
			return err
		}
		if isAdmin {
			for _, arg := range c.Args() {
				isAdded, err := Contains(ctx, cfg, db, RedisChatsAdded, arg)
				if err == nil && !isAdded {
					_ = c.Reply(fmt.Sprintf("Note: seems like bot isn't in the chan %s", arg))
				}
			}
			return Ok(c, SAddT(ctx, cfg, db, RedisSources, c.Args()...))
		}
		return nil
	})
	bot.Handle("/add_chat", func(c telebot.Context) error {
		isAdmin, err := Contains(ctx, cfg, db, RedisAdmins, c.Chat().ID)
		if err != nil {
			return err
		}
		if isAdmin {
			for _, arg := range c.Args() {
				isAdded, err := Contains(ctx, cfg, db, RedisChatsAdded, arg)
				if err == nil && !isAdded {
					_ = c.Reply(fmt.Sprintf("Note: seems like bot isn't in the chat %s", arg))
				}
			}
			return Ok(c, SAddT(ctx, cfg, db, RedisDestinations, c.Args()...))
		}
		return nil
	})
	bot.Handle("/add_admin", func(c telebot.Context) error {
		isAdmin, err := Contains(ctx, cfg, db, RedisAdmins, c.Chat().ID)
		if err != nil {
			return err
		}
		if isAdmin {
			return Ok(c, SAddT(ctx, cfg, db, RedisAdmins, c.Args()...))
		}
		return nil
	})

	bot.Handle("/info", func(c telebot.Context) error {
		isAdmin, err := Contains(ctx, cfg, db, RedisAdmins, c.Chat().ID)
		if err != nil {
			return err
		}

		if isAdmin {
			admins, err := db.SMembers(ctx, Key(cfg.RedisPrefix, RedisAdmins)).Result()
			if err != nil {
				return err
			}
			src, err := db.SMembers(ctx, Key(cfg.RedisPrefix, RedisSources)).Result()
			if err != nil {
				return err
			}
			dst, err := db.SMembers(ctx, Key(cfg.RedisPrefix, RedisDestinations)).Result()
			if err != nil {
				return err
			}

			return c.Reply(fmt.Sprintf(
				"- ADMINS:\n"+
					"%s\n"+
					"---\n"+
					"- REPOST FROM:\n"+
					"%s\n"+
					"---\n"+
					"- REPOST TO:\n"+
					"%s",
				strings.Join(admins, "\n"),
				strings.Join(src, "\n"),
				strings.Join(dst, "\n"),
			))
		}

		return nil
	})
	bot.Handle("/clear", func(c telebot.Context) error {
		isAdmin, err := Contains(ctx, cfg, db, RedisAdmins, c.Chat().ID)
		if err != nil {
			return err
		}

		if isAdmin {
			db.Del(
				ctx,
				Key(cfg.RedisPrefix, RedisAdmins),
				Key(cfg.RedisPrefix, RedisSources),
				Key(cfg.RedisPrefix, RedisDestinations),
				Key(cfg.RedisPrefix, RedisChatsAdded),
			)
		}

		return c.Reply("cleared.\n/setup?")
	})

	bot.Handle("/del_chan", func(c telebot.Context) error {
		isAdmin, err := Contains(ctx, cfg, db, RedisAdmins, c.Chat().ID)
		if err != nil {
			return err
		}
		if isAdmin {
			return Ok(c, SRemT(ctx, cfg, db, RedisSources, c.Args()...))
		}
		return nil
	})
	bot.Handle("/del_chat", func(c telebot.Context) error {
		isAdmin, err := Contains(ctx, cfg, db, RedisAdmins, c.Chat().ID)
		if err != nil {
			return err
		}
		if isAdmin {
			return Ok(c, SRemT(ctx, cfg, db, RedisDestinations, c.Args()...))
		}
		return nil
	})
	bot.Handle("/del_admin", func(c telebot.Context) error {
		isAdmin, err := Contains(ctx, cfg, db, RedisAdmins, c.Chat().ID)
		if err != nil {
			return err
		}
		if isAdmin {
			return Ok(c, SRemT(ctx, cfg, db, RedisAdmins, c.Args()...))
		}
		return nil
	})

	bot.Handle("/setup", func(c telebot.Context) error {
		admins, err := SMembersInt64(ctx, cfg, db, RedisAdmins)
		if err != nil {
			log.Printf("while /setup, %s", err.Error())
			return err
		}
		if len(admins) == 0 {
			return Ok(c, SAddT(ctx, cfg, db, RedisAdmins, c.Sender().ID))
		}
		return nil
	})
	bot.Handle(telebot.OnAddedToGroup, func(c telebot.Context) error {
		return SAddT(ctx, cfg, db, RedisChatsAdded, c.Chat().ID)
	})
	bot.Handle("/ping", func(c telebot.Context) error {
		return c.Reply("/pong")
	})
	bot.Handle("/pong", func(c telebot.Context) error {
		return c.Reply("/ping")
	})

	forward := func(c telebot.Context) error {
		contains, err := Contains(ctx, cfg, db, RedisSources, c.Chat().ID)
		if err != nil {
			return err
		}
		if contains {
			dst, err := SMembersInt64(ctx, cfg, db, RedisDestinations)
			if err != nil {
				return err
			}
			for _, to := range dst {
				err = c.ForwardTo(&telebot.Chat{ID: to})
				time.Sleep(time.Millisecond * 100)
				if err != nil {
					c.Bot().OnError(err, c)
				}
			}
		}
		return nil
	}
	bot.Handle(telebot.OnMedia, forward)
	bot.Handle(telebot.OnText, forward)

	return &Reposter{Bot: bot, DB: db, Context: context.Background()}
}

func Key(values ...string) string {
	return strings.Join(values, ":")
}

func Contains(ctx context.Context, cfg *Config, db *redis.Client, key string, what interface{}) (bool, error) {
	return db.SIsMember(ctx, Key(cfg.RedisPrefix, key), what).Result()
}

func SMembersInt64(ctx context.Context, cfg *Config, db *redis.Client, key string) ([]int64, error) {
	members, err := db.SMembers(ctx, Key(cfg.RedisPrefix, key)).Result()
	if err != nil {
		return nil, err
	}
	ret := make([]int64, len(members))
	for i, str := range members {
		ret[i], err = strconv.ParseInt(str, 10, 64)
		if err != nil {
			return nil, err
		}
	}
	return ret, nil
}

func SAddT[T any](ctx context.Context, cfg *Config, db *redis.Client, key string, what ...T) error {
	for _, el := range what {
		err := db.SAdd(ctx, Key(cfg.RedisPrefix, key), fmt.Sprintf("%v", el)).Err()
		if err != nil {
			return err
		}
	}
	return nil
}

func SRemT[T any](ctx context.Context, cfg *Config, db *redis.Client, key string, what ...T) error {
	for _, el := range what {
		err := db.SRem(ctx, Key(cfg.RedisPrefix, key), fmt.Sprintf("%v", el)).Err()
		if err != nil {
			return err
		}
	}
	return nil
}

func main() {
	argConfig := flag.String("cfg", "", "config path")
	argVerbose := flag.Bool("debug", false, "debug flag")
	flag.Parse()

	cfg, err := ReadConfig(*argConfig)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.RedisPrefix == "" {
		cfg.RedisPrefix = fmt.Sprintf("go-reposter:bot%s", cfg.Token)
	}
	db := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})
	ctx := context.Background()
	bot, err := telebot.NewBot(telebot.Settings{
		Token:       cfg.Token,
		Synchronous: true,
		Verbose:     *argVerbose,
		OnError: func(err error, c telebot.Context) {
			admins, _ := SMembersInt64(ctx, cfg, db, RedisAdmins)
			if err != nil {
				log.Printf("bot %s, error after ReposterGetAdmins, err %v", *argConfig, err)
			}
			for _, admin := range admins {
				_, err = c.Bot().Send(&telebot.Chat{ID: admin}, err.Error())
				if err != nil {
					log.Printf("bot %s, error when sending message to %s, reporting %s", *argConfig, strconv.FormatInt(admin, 10), err.Error())
				}
			}
		},
	})

	reposter := NewReposter(bot, db, ctx, cfg)
	reposter.Bot.Start()
}
