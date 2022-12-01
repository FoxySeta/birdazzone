package tvgames

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"git.hjkl.gq/team13/birdazzone-api/model"
	"git.hjkl.gq/team13/birdazzone-api/tvgames/birdazzone"
	"git.hjkl.gq/team13/birdazzone-api/tvgames/gametracker"
	"git.hjkl.gq/team13/birdazzone-api/tvgames/ghigliottina"
	"git.hjkl.gq/team13/birdazzone-api/twitter"
	"git.hjkl.gq/team13/birdazzone-api/util"
	"github.com/gin-gonic/gin"
	geojson "github.com/paulmach/go.geojson"
	"github.com/swaggo/swag/example/celler/httputil"
)

var gameTrackers = []gametracker.GameTracker{
	ghigliottina.GetGhigliottinaTracker(),
	birdazzone.GetBirdazzoneTracker(),
}

var gameTrackersById = map[int]*gametracker.GameTracker{}

var games []model.Game

var gamesById = map[int]*model.Game{}

const fromDateParsingErrorMessage = "date parsing error (from)"
const toDateParsingErrorMessage = "date parsing error (to)"
const fromGreaterThanToErrorMessage = "from > to"
const toGreaterThenNowErrorMessage = "to > now"

func TvGamesGroup(group *gin.RouterGroup) {
	group.GET("/", getTvGames)
	group.GET("/:id", getTvGameById)
	group.GET("/:id/solution", gameSolution)
	group.GET("/:id/attempts", gameAttempts)
	group.GET("/:id/attempts/stats", gameAttemptsStats)
	group.GET("/:id/results", gameResults)
}

// getTvGames godoc
// @Summary Get all TV games
// @Tags    tvgames
// @Produce json
// @Success 200 {array} model.Game
// @Router  /tvgames [get]
func getTvGames(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, games)
}

// getTvGameById godoc
// @Summary Get TV game
// @Tags    tvgames
// @Produce json
// @Param   id  path     int true "ID to search"
// @Success 200 {object} model.Game
// @Failure 404 {object} model.Error "game id not found"
// @Router  /tvgames/{id} [get]
func getTvGameById(ctx *gin.Context) {
	game, err := util.IdToObject(ctx, gamesById)
	if err == nil {
		ctx.JSON(http.StatusOK, game)
	}
}

// gameSolution godoc
// @Summary Retrieve game's solution
// @Tags    tvgames
// @Produce json
// @Param   id   path     string true  "Game to query"
// @Param   date query    string false "Date to query; if not specified, last game instance is considered" Format(date)
// @Success 200  {object} model.GameKey
// @Success 204  {string} string      "No game instance has been played"
// @Failure 400  {object} model.Error "integer parsing error (id) or error while parsing to date"
// @Failure 404  {object} model.Error "game id not found"
// @Failure 500  {object} model.Error "(internal server error)"
// @Router  /tvgames/{id}/solution [get]
func gameSolution(ctx *gin.Context) {

	gameTracker, err := util.IdToObject(ctx, gameTrackersById)
	if err != nil {
		return
	}

	date_str, hasDate := ctx.GetQuery("date")
	var date time.Time
	var sol model.GameKey
	if hasDate {
		date, err = util.StringToDate(date_str)
		if err != nil {
			httputil.NewError(ctx, http.StatusBadRequest,
				fmt.Errorf("integer parsing error (id) or error while parsing to date"))
			return
		}
		if gameTracker.Solution != nil {
			sol, err = gameTracker.Solution(date)
		} else {
			httputil.NewError(ctx, http.StatusInternalServerError,
				fmt.Errorf("missing solution getter for %T", gameTracker))
		}
	} else {
		if gameTracker.Solution != nil {
			sol, err = gameTracker.LastSolution()
		} else {
			httputil.NewError(ctx, http.StatusInternalServerError,
				fmt.Errorf("missing last solution getter for %T", gameTracker))
		}
	}

	if err == nil {
		ctx.JSON(http.StatusOK, sol)
	} else {
		httputil.NewError(ctx, http.StatusInternalServerError, err)
	}

}

func getAttempts(ctx *gin.Context, successesOnly bool, fromStr string, toStr string) (*twitter.ProfileTweets, error) {
	gameTracker, err := util.IdToObject(ctx, gameTrackersById)
	if err != nil {
		return nil, err
	}
	query := gameTracker.Query

	if fromStr == "" {
		//!from && !to
		lastSol, err := gameTracker.LastSolution()
		if successesOnly {
			if err != nil {
				return nil, err
			}
			query += " " + lastSol.Key
		}
		return twitter.GetManyRecentTweetsFromQuery(query, "", lastSol.Date)
	}
	//from enabled
	t, _ := util.StringToDateTime(fromStr)
	sol, err := gameTracker.Solution(t)
	if successesOnly {
		if err != nil {
			return nil, err
		}
		query += " " + sol.Key
	}
	if sol.Date != t.Format("YYYY-MM-DD") {
		return &twitter.ProfileTweets{Data: []twitter.ProfileTweet{}}, nil
	}
	if sol.Date < toStr || toStr == "" { // from && !to
		toStr = sol.Date
	}
	return twitter.GetManyRecentTweetsFromQuery(query, fromStr, toStr)
}

func toLowerAlphaOnly(r rune) rune {
	if unicode.IsLetter(r) {
		return unicode.ToLower(r)
	}
	if unicode.IsDigit(r) || strings.ContainsRune("#:/@", r) {
		return r
	}
	return ' '
}

var attemptsBlacklist = []string{
	"indovinato",
	"indovinata",
	"perché",
	"perchè",
	"perche'",
	"soluzione",
	"oppure",
	"sicuramente",
	"però",
	"peró",
	"non",
	"stasera",
	"voi",
}

func tweetTextToAttempt(text string) string {
	for _, word := range strings.Split(strings.Map(toLowerAlphaOnly, text), " ") {
		if len(word) >= 3 &&
			word[0:1] != "#" &&
			(len(word) == 3 || word[0:4] != "http") &&
			util.IsAlphabetic(&word) &&
			!util.Contains(&attemptsBlacklist, word) {
			return word
		}
	}
	return ""
}

func getGameAttemptsParameters(ctx *gin.Context) (int, int, string, string, error) {
	var pageIndex, pageLength int
	pageIndex, err := strconv.Atoi(ctx.DefaultQuery("pageIndex", "1"))
	if err != nil {
		httputil.NewError(ctx, http.StatusBadRequest, errors.New("integer parsing error (pageIndex)"))
		return 0, 0, "", "", err
	}
	if pageIndex < 1 {
		httputil.NewError(ctx, http.StatusBadRequest, errors.New("pageIndex < 1"))
		return 0, 0, "", "", err
	}
	pageLength, err = strconv.Atoi(ctx.DefaultQuery("pageLength", "1"))
	if err != nil {
		httputil.NewError(ctx, http.StatusBadRequest, errors.New("integer parsing error (pageLength)"))
		return 0, 0, "", "", err
	}
	if pageLength < 1 {
		httputil.NewError(ctx, http.StatusBadRequest, errors.New("pageLength < 1"))
		return 0, 0, "", "", err
	}

	fromStr, hasFrom := ctx.GetQuery("from")
	toStr, hasTo := ctx.GetQuery("to")
	var fromTime, toTime time.Time
	if hasFrom {
		fromTime, err = util.StringToDateTime(fromStr)
		if err != nil {
			httputil.NewError(ctx, http.StatusBadRequest, errors.New(fromDateParsingErrorMessage))
			return 0, 0, "", "", err
		}
		fromStr = util.DateToString(fromTime)
		if hasTo {
			toTime, err = util.StringToDateTime(toStr)
			if err != nil {
				httputil.NewError(ctx, http.StatusBadRequest, errors.New(toDateParsingErrorMessage))
				return 0, 0, "", "", err
			}
			toStr = util.DateToString(toTime)
			if fromStr > toStr {
				httputil.NewError(ctx, http.StatusBadRequest, errors.New(fromGreaterThanToErrorMessage))
				return 0, 0, "", "", err
			}
			if toStr > util.DateToString(time.Now()) {
				httputil.NewError(ctx, http.StatusBadRequest, errors.New(toGreaterThenNowErrorMessage))
				return 0, 0, "", "", err
			}
			if fromTime.Day() != toTime.Day() || fromTime.Month() != toTime.Month() || fromTime.Year() != toTime.Year() {
				httputil.NewError(ctx, http.StatusBadRequest, errors.New("from and to are not in the same day"))
				return 0, 0, "", "", err
			}
		}
	} else {
		fromStr = ""
		toStr = ""
	}
	return pageIndex, pageLength, fromStr, toStr, nil
}

// gameAttempts godoc
// @Summary Retrieve game's attempts
// @Tags    tvgames
// @Produce json
// @Param   id         path     string true  "Game to query"
// @Param   from       query    string false "Starting instant of the time interval used to filter the tweets. If not specified, the beginning of the last game instance is used"                                                                                    Format(date-time)
// @Param   to         query    string false "Ending instant of the time interval used to filter the tweets. Must be later than but in the same day of the starting instant. If not specified, the ending of the game happening during the starting instant is used" Format(date-time)
// @Param   pageIndex  query    int    false "Index of the page to query"                                                                                                                                                                                            minimum(1) default(1)
// @Param   pageLength query    int    false "Length of the page to query"                                                                                                                                                                                           minimum(1) default(10)
// @Success 200        {object} model.Page[model.Tweet]
// @Success 204        {string} string      "no game instance has been played"
// @Failure 400        {object} model.Error "integer parsing error (pageIndex) or pageIndex < 1 or integer parsing error (pageLength) or pageIndex < pageLength or integer parsing error (id) or error while parsing to date"
// @Failure 404        {object} model.Error "game id not found"
// @Failure 500        {object} model.Error "(internal server error)"
// @Router  /tvgames/{id}/attempts [get]
func gameAttempts(ctx *gin.Context) {
	pageIndex, pageLength, fromStr, toStr, err := getGameAttemptsParameters(ctx)
	if err != nil {
		return
	}
	result, err := getAttempts(ctx, true, fromStr, toStr)
	if err != nil {
		httputil.NewError(ctx, http.StatusNoContent, errors.New("no game instance has been played"))
		return
	}
	tweets := result.Data
	util.Reverse(&tweets)
	// users
	usersById := make(map[string]twitter.Profile, len(result.Includes.Users))
	for _, user := range result.Includes.Users {
		usersById[user.ID] = user
	}
	// places
	placesById := make(map[string]twitter.Place, len(result.Includes.Places))
	for _, place := range result.Includes.Places {
		placesById[place.ID] = place
	}
	n := len(tweets)
	from := util.Max(0, util.Min(pageLength*(pageIndex-1), n-1))
	res := make([]model.Tweet, util.Min(from+pageLength, n)-from)
	for i := range res {
		tweet := tweets[from+i]
		var geometry *geojson.Geometry
		if tweet.Geo != nil {
			geo := placesById[tweet.Geo.PlaceId].Geo
			geometry = &geo
		}
		res[i] = model.MakeTweet(tweet, usersById[tweet.AuthorId], geometry)
	}
	ctx.JSON(http.StatusOK, model.Page[model.Tweet]{Entries: res, NumberOfPages: (n + pageLength - 1) / pageLength})
}

func getAttemptsStats(ctx *gin.Context) (model.Chart, error) {
	gameTracker, err := util.IdToObject(ctx, gameTrackersById)
	if err != nil {
		return nil, err
	}
	fromStr, hasFrom := ctx.GetQuery("from")
	toStr, hasTo := ctx.GetQuery("to")
	var fromTime, toTime time.Time
	if hasFrom {
		fromTime, err = util.StringToDateTime(fromStr)
		if err != nil {
			return nil, errors.New(fromDateParsingErrorMessage)
		}
		fromStr = util.DateToString(fromTime)
		if hasTo {
			toTime, err = util.StringToDateTime(toStr)
			if err != nil {
				return nil, errors.New(toDateParsingErrorMessage)
			}
			toStr = util.DateToString(toTime)
			if fromStr > toStr {
				return nil, errors.New(fromGreaterThanToErrorMessage)
			}
			if toStr > util.DateToString(time.Now()) {
				return nil, errors.New(toGreaterThenNowErrorMessage)
			}
			if fromTime.Day() != toTime.Day() || fromTime.Month() != toTime.Month() || fromTime.Year() != toTime.Year() {
				return nil, errors.New("from and to are not in the same day")
			}
		}
	} else {
		fromStr = ""
		toStr = ""
	}

	result, err := getAttempts(ctx, false, fromStr, toStr)
	if err != nil {
		return nil, err
	}
	tweets := result.Data
	var solution model.GameKey
	if fromStr != "" {
		s, _ := util.StringToDateTime(fromStr)
		solution, err = gameTracker.Solution(s)
	} else {
		solution, err = gameTracker.LastSolution()
	}

	if err != nil {
		return nil, err
	}
	chartAsMap := make(map[string]int)
	for _, tweet := range tweets {
		text := strings.ToLower(tweet.Text)
		var attempt string
		if strings.Contains(text, solution.Key) {
			attempt = solution.Key
		} else {
			attempt = tweetTextToAttempt(text)
		}
		if attempt == "" {
			continue
		}
		_, ok := chartAsMap[attempt]
		if ok {
			chartAsMap[attempt]++
		} else {
			chartAsMap[attempt] = 1
		}
	}
	chart := make(model.Chart, len(chartAsMap))
	i := 0
	for k, v := range chartAsMap {
		chart[i] = model.ChartEntry{Value: k, AbsoluteFrequency: v}
		i++
	}
	return chart, nil
}

// gameAttemptsStats godoc
// @Summary Retrieve game's attempts' frequencies
// @Tags    tvgames
// @Produce json
// @Param   id   path     string true  "Game to query"
// @Param   from query    string false "Starting instant of the time interval used to filter the tweets. If not specified, the beginning of the last game instance is used"                                                                                    Format(date-time)
// @Param   to   query    string false "Ending instant of the time interval used to filter the tweets. Must be later than but in the same day of the starting instant. If not specified, the ending of the game happening during the starting instant is used" Format(date-time)
// @Success 200  {object} model.Chart
// @Success 204  {string} string      "No game instance has been played"
// @Failure 400  {object} model.Error "integer parsing error (id) or error while parsing to date"
// @Failure 404  {object} model.Error "game id not found"
// @Router  /tvgames/{id}/attempts/stats [get]
func gameAttemptsStats(ctx *gin.Context) {
	chart, err := getAttemptsStats(ctx)
	if err == nil {
		ctx.JSON(http.StatusOK, chart)
	} else {
		httputil.NewError(ctx, http.StatusBadRequest, err)
	}
}

func gameResultsHelper(solution model.GameKey, tweets *[]twitter.ProfileTweet, each int, chart []model.BooleanChart, err error, updateNextTime bool) []model.BooleanChart {
	if err == nil {
		successes := 0
		fails := 0
		lt, _ := util.StringToDateTime((*tweets)[0].CreatedAt)
		lastTime := util.DateToString(time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour(), lt.Minute(), lt.Second()+each, 0, time.UTC))
		for _, tweet := range *tweets {
			if tweet.CreatedAt <= lastTime {
				if strings.Contains(strings.ToLower(tweet.Text), solution.Key) {
					successes++
				} else {
					fails++
				}
			} else {
				nt, _ := util.StringToDateTime(lastTime)
				// nextTime := util.DateToString(time.Date(nt.Year(), nt.Month(), nt.Day(), nt.Hour(), nt.Minute(), nt.Second()+each, 0, time.UTC))
				chart = append(chart, model.BooleanChart{Label: lastTime, Positives: successes, Negatives: fails})
				nt, _ = util.StringToDateTime(tweet.CreatedAt)
				if updateNextTime {
					// nextTime = util.DateToString(nt)
				}
				lastTime = util.DateToString(nt)
				successes = 0
				fails = 0
				if strings.Contains(strings.ToLower(tweet.Text), solution.Key) {
					successes++
				} else {
					fails++
				}
			}

		}
		// nextTime, _ := util.StringToDateTime((*tweets)[len(*tweets)-1].CreatedAt)
		chart = append(chart, model.BooleanChart{Label: lastTime, Positives: successes, Negatives: fails})
	}
	return chart
}

// gameResults godoc
// @Summary Retrieve game's number of successes and failures, grouped in time interval bins
// @Tags    tvgames
// @Produce json
// @Param   id   path     string             true  "Game to query"
// @Param   from query    string             false "Starting date of the time interval used to filter the tweets. If not specified, the last game instance's date is used"                             Format(date)
// @Param   to   query    string             false "Ending date of the time interval used to filter the tweets. Cannot be earlier than the starting date. If not specified, the starting date is used" Format(date)
// @Param   each query    int                false "Number of seconds for the duration of each time interval bin the retrieved tweets are to be grouped by"                                            minimum(1)
// @Success 200  {array}  model.BooleanChart "An array of boolean charts comparing successes and failures in the game. Each boolean chart is labeled as the starting instant of its time interval bin"
// @Success 204  {string} string             "no game instance has been played"
// @Failure 400  {object} model.Error        "integer parsing error (id) or date parsing error (from) or date parsing error (to) or to > today or from > to or integer parsing error (each) or each < 1"
// @Failure 404  {object} model.Error        "game id not found"
// @Router  /tvgames/{id}/results [get]
func gameResults(ctx *gin.Context) {
	gameTracker, err := util.IdToObject(ctx, gameTrackersById)
	var chart []model.BooleanChart

	if err == nil {
		fromStr, hasFrom := ctx.GetQuery("from")
		toStr, hasTo := ctx.GetQuery("to")
		eachStr, hasEach := ctx.GetQuery("each")
		var each = 57600
		var fromTime, toTime time.Time
		if hasEach {
			each, err = strconv.Atoi(eachStr)
			if err != nil {
				httputil.NewError(ctx, http.StatusBadRequest, errors.New("integer parsing error (each)"))
				return
			}
			if each < 1 {
				httputil.NewError(ctx, http.StatusBadRequest, errors.New("each < 1"))
				return
			}
		}

		if hasFrom {
			fromTime, err = util.StringToDate(fromStr)
			if err != nil {
				httputil.NewError(ctx, http.StatusBadRequest, errors.New(fromDateParsingErrorMessage))
				return
			}
			fromStr = util.DateToString(fromTime)
			if hasTo {
				//from && to
				toTime, err = util.StringToDate(toStr)
				if err != nil {
					httputil.NewError(ctx, http.StatusBadRequest, errors.New(toDateParsingErrorMessage))
					return
				}
				toStr = util.DateToString(toTime)
				if fromStr > toStr {
					httputil.NewError(ctx, http.StatusBadRequest, errors.New(fromGreaterThanToErrorMessage))
					return
				}
				if toStr > util.DateToString(time.Now()) {
					httputil.NewError(ctx, http.StatusBadRequest, errors.New(toGreaterThenNowErrorMessage))
					return
				}
			} else {
				//from && !to
				now := time.Now()
				toTime = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			}
		} else {
			//!from && !to
			result, err := getAttempts(ctx, false, "", "")
			if err == nil {
				util.Reverse(&result.Data)
				tweets := result.Data
				var solution model.GameKey
				solution, err = gameTracker.LastSolution()
				if err == nil {
					chart = gameResultsHelper(solution, &tweets, each, chart, err, true)
					ctx.JSON(
						http.StatusOK,
						chart,
					)
				}
			}
			return
		}

		for util.DateToString(fromTime) <= util.DateToString(toTime) {
			result, err := getAttempts(ctx, false, util.DateToString(time.Date(fromTime.Year(), fromTime.Month(), fromTime.Day(), 0, 0, 0, 0, time.UTC)), util.DateToString(time.Date(fromTime.Year(), fromTime.Month(), fromTime.Day(), 23, 59, 59, 0, time.UTC)))
			if err == nil {
				util.Reverse(&result.Data)
				tweets := result.Data
				var solution model.GameKey
				solution, err = gameTracker.Solution(fromTime)
				chart = gameResultsHelper(solution, &tweets, each, chart, err, false)
			}
			fromTime = fromTime.AddDate(0, 0, 1)
		}
		ctx.JSON(
			http.StatusOK,
			chart,
		)
		return
	}
	httputil.NewError(ctx, http.StatusInternalServerError, err)
}

func assignIds() {
	for i := range gameTrackers {
		gameTrackers[i].Game.Id = i
	}
}

func initDataStructures() {
	games = make([]model.Game, len(gameTrackers))
	i := 0
	for k := range gameTrackers {
		v := &gameTrackers[k]
		gameTrackersById[k] = v
		games[i] = v.Game
		gamesById[k] = &(v.Game)
		i += 1
	}
}

func init() {
	assignIds()
	initDataStructures()
}
