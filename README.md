# IF3110 Web Application Development Project 

## Overview

### Milestone 1

In this milestone, we implemented a database web platform where a team can works with several databases from one place, without handing out raw database credentials.

## Build and Run

### Steps

1. Generate secrets to fill in environment variables.
  ```sh
  openssl rand -base64 32
  ```
2. Create a `.env` file from the template and copy  the secret to environment variables.
  ```sh
  cp .env.example .env
  # then open the file and fill `change-me` parts with actual secrets 
  ```
3. Build the container and start the server.
  ```sh
  docker compose up --build
  ```
4. Open **[https://localhost](https://localhost)**. Ignore browser warning and proceed. Caddy uses a self-signed internal certificate, so accept the browser warning, or trust Caddy's root CA. The app container is not published on a host port. All traffic goes through Caddy on 80/443.

### Environment Variables

## Project Structure

### Architectural Diagram

### Repository Structure

## Features and Behavior

## Credits

**IF3110 Web Application Development**  
*Semester 1 2026/2027*

Group K03-01: 
| Nama                        | NIM      | Username                                         |
|-----------------------------|----------|--------------------------------------------------|
| Dzaki Ahmad Al Hussainy     | 13524084 | [@HussainDzaki](https://github.com/HussainDzaki) |
| Moreno Syawali Ganda Sugita | 13524096 | [@DeSince29](https://github.com/DeSince29)       |
| Muhammad Akmal              | 13524099 | [@m-akma1](https://github.com/m-akma1)           |

**Program Studi Teknik Informatika**  
**Sekolah Teknik Elektro dan Informatika**  
**Institut Teknologi Bandung**