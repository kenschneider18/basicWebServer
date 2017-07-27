package main

import (
	"net/http"
	"fmt"
	"encoding/json"
	"strings"
	"time"
	"strconv"
	"bytes"
	"io"
)

const (
	OpenApiKey = "8c7374560763424676ccbf5a721b56e0"
)

type weatherProvider interface {
	tempAndHumidity(city string) (temp float64, humidity int, err error)
}

type weatherData struct {
	Name string `json:"name"`
	Main struct {
		Kelvin float64 `json:"temp"`
		Humidity int `json:"humidity"`
	} `json:"main"`
}

func main() {
	mw := multiWeatherProvider{
		openWeatherMap{}, // we can do this because both openWeatherMap and weatherUnderground conform to the weatherProvider interface
		weatherUnderground{apiKey: "ff7be5e9c4c5864b"},
	}

	http.HandleFunc("/hello", hello)
	http.HandleFunc("/weather/", func (w http.ResponseWriter, r *http.Request) {
		city := strings.SplitN(r.URL.Path, "/", 3)[2]

		data, err := query(city)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(data)
	})
	http.HandleFunc("/multiweather/", func (w http.ResponseWriter, r *http.Request) {
		begin := time.Now()
		city := strings.SplitN(r.URL.Path, "/", 3)[2]

		temp, humidity, err := mw.tempAndHumidity(city)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"city": city,
			"temp": temp,
			"humidity": humidity,
			"took": time.Since(begin).String(),
		})
	})


	fmt.Println("Started server on port 8080!")
	http.ListenAndServe(":8080", nil)
}

func query(city string) (weatherData, error) {
	resp, err := http.Get("http://api.openweathermap.org/data/2.5/weather?APPID=" + OpenApiKey + "&q=" + city)
	if err != nil {
		return weatherData{}, err
	}

	// Defer functions are dope
	defer resp.Body.Close()

	var d weatherData
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return weatherData{}, err
	}

	return d, nil
}

type openWeatherMap struct{}

func (sw openWeatherMap) tempAndHumidity(city string) (temp float64, humidity int, err error) {
	fmt.Println("Open Weather map API call started...")
	resp, err := http.Get("http://api.openweathermap.org/data/2.5/weather?APPID=" + OpenApiKey + "&q=" + city)
	if err != nil {
		return 0, 0, err
	}

	// Defer functions are dope
	defer resp.Body.Close()

	body, err := log("OpenWeatherMap", resp.StatusCode, resp.Body)
	if err != nil {
		panic(fmt.Errorf("Could not log OpenWeatherMap response: %s", err))
	}

	var d weatherData
	if err := json.NewDecoder(body).Decode(&d); err != nil {
		return 0, 0, err
	}

	return d.Main.Kelvin, d.Main.Humidity, nil
}

type weatherUnderground struct{
	apiKey string
}

func (w weatherUnderground) tempAndHumidity(city string) (temp float64, humidity int, err error) {
	fmt.Println("Weather Underground API call started...")
	resp, err := http.Get("http://api.wunderground.com/api/" + w.apiKey + "/conditions/q/" + city + ".json")
	if err != nil {
		return 0,0, err
	}

	defer resp.Body.Close()

	body, err := log("WeatherUnderground", resp.StatusCode, resp.Body)
	if err != nil {
		panic(fmt.Errorf("Could not log response from WeatherUnderground: %s", err))
	}

	var d struct {
		Observation struct {
			Celsius float64 `json:"temp_c"` // these are tags, useful for different conversions including json and protocol buffers
			Humidity string `json:"relative_humidity"`
		} `json:"current_observation"`
	}

	// This gets executed on a io.Reader and reads that info into the provided stuct
	// so long as we have the correct tags on it
	if err := json.NewDecoder(body).Decode(&d); err != nil {
		return 0, 0, err
	}

	temp = d.Observation.Celsius + 273.15

	humidity, err = strconv.Atoi(d.Observation.Humidity[:2]) // Get rid of the % on the end, this will fail with 100% humidity
	if err != nil {
		return temp, 0, err
	}
	return temp, humidity, nil
}

// Turn it into a type because then we can create a method for it
// and call it on the type so it's kind of like alias in C but better
type multiWeatherProvider []weatherProvider

// Use this so we can make one channel and grab our ish from it
type weatherTuple struct {
	temp float64
	humidity int
}

// method on []weatherProvider with the alias multiWeatherProvider
func (mw multiWeatherProvider) tempAndHumidity(city string) (temp float64, humidity int, err error) {
	totalTemp := 0.0
	totalHumidity := 0

	//temps := make(chan float64, len(mw))
	//humidities := make(chan int, len(mw))
	data := make(chan weatherTuple, len(mw))
	errors := make(chan error, len(mw))

	for _, provider := range mw {
		/*k, h, err := provider.tempAndHumidity(city)
		if err != nil {
			return 0, 0, err
		}

		totalTemp += k
		totalHumidity += h*/

		go func (p weatherProvider) {
			k, h, err := p.tempAndHumidity(city)
			if err != nil {
				errors <- err
			}
			data <- weatherTuple{temp:k, humidity:h}
		}(provider) // by adding the (provider) at the end we call the anonymous function, passing the variable
	}

	for i := 0; i < len(mw); i++ {
		select {
		case stuff := <-data:
			totalTemp += stuff.temp
			totalHumidity += stuff.humidity
		case err := <-errors:
			return 0, 0, err
		}
	}

	avgKelvin := totalTemp / float64(len(mw))
	fmt.Printf("Avg Kelvin: %f\n", avgKelvin)

	return avgKelvin * 1.8 - 459.67, totalHumidity / len(mw), nil
}

func log(name string, statusCode int, body io.ReadCloser) (io.Reader, error) {
	var buf bytes.Buffer
	tee := io.TeeReader(body, &buf)

	var printBuf bytes.Buffer
	_, err := printBuf.ReadFrom(tee)
	if err != nil {
		//panic(fmt.Errorf("Could not read from TeeReader: %s", err))
		return nil, err
	}
	bodyString := printBuf.String()

	fmt.Printf("Received %d response from %s:\n%s\n", statusCode, name, bodyString)

	return bytes.NewReader(buf.Bytes()), nil
}

func hello(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("hello!"))
}
