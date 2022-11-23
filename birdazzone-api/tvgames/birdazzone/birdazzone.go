package birdazzone

import (
	"errors"
	"strings"
	"time"

	"git.hjkl.gq/team13/birdazzone-api/model"
	"git.hjkl.gq/team13/birdazzone-api/tvgames/gametracker"
	"git.hjkl.gq/team13/birdazzone-api/twitter"
	"git.hjkl.gq/team13/birdazzone-api/util"
)

var birdazzoneTracker = gametracker.GameTracker{
	Game: model.Game{
		Name:    "Birdazzone",
		Hashtag: "#birdazzone",
		Logo:    "/public/birdazzone.png"},
	Query:        "#birdazzone -from:birdazzone -is:retweet",
	Solution:     givenSolution,
	LastSolution: lastSolution,
}

func GetBirdazzoneTracker() gametracker.GameTracker {
	return birdazzoneTracker
}

func solution(start_time string, end_time string) (model.GameKey, error) {
	tweets, err := twitter.GetRecentTweetsFromQuery("La soluzione al #birdazzone di oggi", start_time, end_time, 10)

	if err != nil {
		return model.GameKey{}, err
	}
	if tweets.Meta.ResultCount == 0 {
		return model.GameKey{}, errors.New("couldn't find Birdazzone solution")
	}
	text := tweets.Data[0].Text
	a := strings.ToLower(text[strings.LastIndex(text, " ")+1:])
	if len(a) > 0 {
		return model.GameKey{
			Key:  a,
			Date: tweets.Data[0].CreatedAt,
		}, nil
	}
	return model.GameKey{}, errors.New("couldn't find Birdazzone solution")
}

func givenSolution(dt time.Time) (model.GameKey, error) {
	start_time := util.LastInstantAtGivenTime(dt, 0)
	end_time := util.LastInstantAtGivenTime(dt.AddDate(0, 0, 1), 0)
	if end_time > util.DateToString(time.Now()) {
		end_time = ""
	}
	return solution(start_time, end_time)
}

func lastSolution() (model.GameKey, error) {
	return solution("", "")
}
