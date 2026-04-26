# Multi-Agent Drone Rescue Simulation

This project implements a distributed multi-agent system to simulate coordination of drones in a search and rescue scenario.

## Overview

The goal is to model how multiple autonomous agents (drones) can coordinate in a dynamic environment to efficiently explore an area and locate targets.

The system focuses on:
- distributed decision-making
- agent coordination strategies
- concurrent execution and communication

## Features

- Multi-agent simulation in a dynamic environment
- Concurrent execution using Go (goroutines)
- Communication between agents
- Exploration and coordination logic

## Tech Stack

- Go (backend simulation, concurrency)
- JavaScript (visualization)

## Usage

### Run simulation

```bash
go run .

### Run simulation
$env:TRAIN_BATCH="1"
go run .
