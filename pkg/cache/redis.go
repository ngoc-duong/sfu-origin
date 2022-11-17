package cacheredis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

var ctx = context.Background()

var PushPeers []string

var redisClient = redis.NewClient(&redis.Options{
	Addr:     "localhost:6379",
	Password: "",
	DB:       0,
})

func GetCacheRedis(key string) ([]string, error) {
	var result []string
	var err error
	ssiCache, errGet := redisClient.Get(ctx, key).Result()
	err = errGet
	if ssiCache != "" {
		errPaser := json.Unmarshal([]byte(ssiCache), &result)
		err = errPaser
	}
	return result, err
}

func SetCacheRedis(key string, value string) error {
	fmt.Println("Set cache redis")
	var result []string
	var err error
	ssiCache, errGet := redisClient.Get(ctx, key).Result()
	err = errGet
	if ssiCache != "" {
		errPaser := json.Unmarshal([]byte(ssiCache), &result)
		err = errPaser
	}

	checkExist := false
	for _, ss := range result {
		if ss == value {
			checkExist = true
			break
		}
	}
	if checkExist == false {
		result = append(result, value)
	}

	resultJson, errJson := json.Marshal(result)
	if errJson != nil {
		return errJson
	}
	errSet := redisClient.Set(ctx, key, string(resultJson), time.Hour*1).Err()
	err = errSet
	return err
}

func DeleteCache(key string, value string) error {
	var result []string
	var err error
	ssiCache, errGet := redisClient.Get(ctx, key).Result()
	err = errGet
	if ssiCache != "" {
		errPaser := json.Unmarshal([]byte(ssiCache), &result)
		err = errPaser
	}
	var newResult []string
	for _, v := range result {
		if v != value {
			newResult = append(newResult, v)
		}
	}
	resultJson, errJson := json.Marshal(newResult)
	if errJson != nil {
		return errJson
	}
	errSet := redisClient.Set(ctx, key, string(resultJson), time.Hour*1).Err()
	err = errSet
	return err
}

func DeleteAll(key string) error {
	err := redisClient.Del(ctx, key).Err()
	return err
}
