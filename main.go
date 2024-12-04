package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"golang.ngrok.com/ngrok"
	"golang.ngrok.com/ngrok/config"
)

type WeatherResponse struct {
	Main struct {
		Temp      float64 `json:"temp"`
		FeelsLike float64 `json:"feels_like"`
		Humidity  int     `json:"humidity"`
		Pressure  int     `json:"pressure"`
	} `json:"main"`
	Weather []struct {
		Description string `json:"description"`
	} `json:"weather"`
	Wind struct {
		Speed float64 `json:"speed"`
	} `json:"wind"`
}

type InlineKeyboardButton struct {
	Text   string     `json:"text"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
}

type WebAppInfo struct {
	URL string `json:"url"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type SendMessageRequest struct {
	ChatID      int64                `json:"chat_id"`
	Text        string               `json:"text"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

var botToken string
var publicURL string

func init() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}
	botToken = os.Getenv("BOT_TOKEN")
}

func main() {
	// Настройка обработки только новых сообщений
	lastUpdateID := 0

	// Запуск HTTP сервера для Mini App
	r := mux.NewRouter()
	r.HandleFunc("/", serveHTML)
	r.HandleFunc("/weather/{city}", getWeather)
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Настройка ngrok
	ctx := context.Background()
	listener, err := ngrok.Listen(ctx,
		config.HTTPEndpoint(),
		ngrok.WithAuthtoken(os.Getenv("NGROK_AUTHTOKEN")),
	)
	if err != nil {
		log.Fatal(err)
	}

	publicURL = listener.URL()
	log.Printf("Mini App доступно по адресу: %s", publicURL)

	go func() {
		if err := http.Serve(listener, r); err != nil {
			log.Fatal(err)
		}
	}()

	// Основной цикл бота
	for {
		updates, err := getUpdates(lastUpdateID + 1)
		if err != nil {
			log.Printf("Error getting updates: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, update := range updates {
			if update.Message != nil && update.Message.Text == "/start" {
				// Создаем inline клавиатуру
				keyboard := &InlineKeyboardMarkup{
					InlineKeyboard: [][]InlineKeyboardButton{
						{
							{
								Text: "Открыть Weather App",
								WebApp: &WebAppInfo{
									URL: publicURL,
								},
							},
						},
					},
				}

				err := sendMessage(update.Message.Chat.ID, "Привет! Нажми на кнопку ниже, чтобы открыть Weather Mini App", keyboard)
				if err != nil {
					log.Printf("Error sending message: %v", err)
				}
			}
			if update.UpdateID >= lastUpdateID {
				lastUpdateID = update.UpdateID
			}
		}

		time.Sleep(1 * time.Second)
	}
}

type Update struct {
	UpdateID int     `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	Chat *Chat  `json:"chat"`
	Text string `json:"text"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type UpdateResponse struct {
	Ok     bool     `json:"ok"`
	Result []Update `json:"result"`
}

func getUpdates(offset int) ([]Update, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=60", botToken, offset)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var updateResp UpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&updateResp); err != nil {
		return nil, err
	}

	return updateResp.Result, nil
}

func sendMessage(chatID int64, text string, keyboard *InlineKeyboardMarkup) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	
	msg := SendMessageRequest{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: keyboard,
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("telegram API error: %s", string(body))
	}

	return nil
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/index.html")
}

func getWeather(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	city := vars["city"]

	apiKey := os.Getenv("WEATHER_API_KEY")
	url := fmt.Sprintf("http://api.openweathermap.org/data/2.5/weather?q=%s&appid=%s&units=metric&lang=ru", city, apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        json.NewEncoder(w).Encode(ErrorResponse{Error: "Ошибка при получении данных о погоде"})
        return
    }
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusNotFound)
        json.NewEncoder(w).Encode(ErrorResponse{Error: "Город не найден. Проверьте правильность написания."})
        return
    }

	if resp.StatusCode != http.StatusOK {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(resp.StatusCode)
        json.NewEncoder(w).Encode(ErrorResponse{Error: "Ошибка при получении данных о погоде"})
        return
    }

	var weather WeatherResponse
	if err := json.NewDecoder(resp.Body).Decode(&weather); err != nil {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusInternalServerError)
        json.NewEncoder(w).Encode(ErrorResponse{Error: "Ошибка при обработке данных о погоде"})
        return
    }

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(weather)
}
