# TGAgent

A Telegram bot built with Go that integrates with FastGPT API.

## Features
- Command management
- Action handling
- FastGPT integration
- PostgreSQL database support
- Proxy support

## Configuration
1. Copy `config.example.json` to `config.json`
2. Set up environment variables in `.env`
3. Configure your database settings

## Installation

### Standard Installation
```bash
go mod download
go build -o bot
```

### Docker Installation
```bash
# Build the image
docker build -t tom-jerry-bot .

# Run the container
docker run -d \
  --name tom-jerry-bot \
  -p 8080:8080 \
  --env-file .env \
  tom-jerry-bot
```

## Usage
```bash
./bot
```

## Environment Variables
- `DB_PASSWORD`: Database password
- `FASTGPT_API_KEY`: FastGPT API key
- `HTTP_PROXY`: HTTP proxy URL (optional)
- `HTTPS_PROXY`: HTTPS proxy URL (optional)

## License
MIT