# Sybil API Documentation

## Base URL
The base URL for all API endpoints is:

`https://mx-central-02.sybil.com`

## Endpoints

### 1. Ping
Check if the server is running.

- **URL:** `https://mx-central-02.sybil.com/ping`
- **Method:** GET
- **Response:**
  - **Status Code:** 200
  - **Body:** Empty string

### 2. Search Images
Search for images based on a query.

- **URL:** `https://mx-central-02.sybil.com/search/images`
- **Method:** POST
- **Headers:**
  - Content-Type: application/json
- **Request Body:**
  ```json
  {
    "query": "string",
    "page": integer
  }
  ```
- **Response:**
  - **Status Code:** 200
  - **Body:** JSON array of image search results
- **Error Responses:**
  - **Status Code:** 400 (Bad Request)
    - Occurs when the request body is invalid
  - **Status Code:** 500 (Internal Server Error)
    - Occurs when there's an error querying the search engine

### 3. Search
Perform a general search and receive results as server-sent events.

- **URL:** `https://mx-central-02.sybil.com/search`
- **Method:** POST
- **Headers:**
  - Content-Type: application/json
  - X-SESSION-ID: string (optional, used for saving the answer)
- **Request Body:**
  ```json
  {
    "query": "string"
  }
  ```
- **Response:**
  - **Status Code:** 200
  - **Content-Type:** text/event-stream
  - **Events:**
    1. Sources Event:
       ```json
       {
         "type": "sources",
         "sources": [/* array of search results */]
       }
       ```
    2. Related Suggestions Event:
       ```json
       {
         "type": "related",
         "followups": [/* array of related search suggestions */]
       }
       ```
    3. Hero Card Event (if available):
       ```json
       {
         "type": "heroCard",
         "heroCard": {
           "type": "news",
           "url": "string",
           "image": "string",
           "title": "string",
           "intro": "string",
           "size": "auto"
         }
       }
       ```
- **Error Responses:**
  - **Status Code:** 400 (Bad Request)
    - Occurs when no query is provided
  - **Status Code:** 500 (Internal Server Error)
    - Occurs when there's an error processing the search

### 4. Autocomplete
Get autocomplete suggestions for a search query.

- **URL:** `https://mx-central-02.sybil.com/autocomplete`
- **Method:** GET
- **Query Parameters:**
  - q: string (the partial search query)
- **Response:**
  - **Status Code:** 200
  - **Body:** JSON array of autocomplete suggestions
- **Error Response:**
  - **Status Code:** 500 (Internal Server Error)
    - Occurs when there's an error fetching autocomplete suggestions

### 5. Search Sources
Search for general sources based on a query.

- **URL:** `https://mx-central-02.sybil.com/search/sources`
- **Method:** POST
- **Headers:**
  - Content-Type: application/json
- **Request Body:**
  ```json
  {
    "query": "string",
    "page": integer
  }
  ```
- **Response:**
  - **Status Code:** 200
  - **Body:** JSON array of search results
- **Error Responses:**
  - **Status Code:** 400 (Bad Request)
    - Occurs when the request body is invalid
  - **Status Code:** 500 (Internal Server Error)
    - Occurs when there's an error querying the search engine

## Notes
- The API uses server-sent events for the `/search` endpoint to stream results in real-time.
- Ensure that your client is configured to handle server-sent events when using the `/search` endpoint.

## Test Script
Below is a Python script to test the main endpoints of the Sybil API:

```python
import requests
import json

# Base URL for the API
BASE_URL = "https://mx-central-02.sybil.com"

def test_ping():
    response = requests.get(f"{BASE_URL}/ping")
    print(f"Ping Test: {'Successful' if response.status_code == 200 else 'Failed'}")
    print(f"Status Code: {response.status_code}")
    print(f"Response: {response.text}\n")

def test_search_images():
    payload = {
        "query": "cute cats",
        "page": 1
    }
    response = requests.post(f"{BASE_URL}/search/images", json=payload)
    print("Search Images Test:")
    print(f"Status Code: {response.status_code}")
    print(f"Response: {json.dumps(response.json(), indent=2)[:200]}...\n")

def test_search():
    payload = {
        "query": "artificial intelligence"
    }
    headers = {
        "X-SESSION-ID": "test-session-id",
        "Content-Type": "application/json"
    }
    print("Search Test (Streaming):")
    with requests.post(f"{BASE_URL}/search", json=payload, headers=headers, stream=True) as response:
        for line in response.iter_lines():
            if line:
                decoded_line = line.decode('utf-8')
                if decoded_line.startswith('data:'):
                    try:
                        data = json.loads(decoded_line[5:])
                        print(f"Event Type: {data.get('type')}")
                        print(f"Data: {json.dumps(data, indent=2)[:200]}...")
                    except json.JSONDecodeError:
                        print(f"Non-JSON data: {decoded_line}")
    print()

def test_autocomplete():
    params = {
        "q": "machine learn"
    }
    response = requests.get(f"{BASE_URL}/autocomplete", params=params)
    print("Autocomplete Test:")
    print(f"Status Code: {response.status_code}")
    print(f"Response: {json.dumps(response.json(), indent=2)}\n")

def test_search_sources():
    payload = {
        "query": "latest news",
        "page": 1
    }
    response = requests.post(f"{BASE_URL}/search/sources", json=payload)
    print("Search Sources Test:")
    print(f"Status Code: {response.status_code}")
    print(f"Response: {json.dumps(response.json(), indent=2)[:200]}...\n")

if __name__ == "__main__":
    test_ping()
    test_search_images()
    test_search()
    test_autocomplete()
    test_search_sources()
```

To use this test script:

1. Ensure you have the `requests` library installed:
   ```
   pip install requests
   ```

2. Save the script as `test_sybilapi.py`.

3. Run the script:
   ```
   python test_sybilapi.py
   ```

This script will test all the main endpoints of the Sybil API and print out the results, including status codes and a portion of the responses for each endpoint.
