#!/bin/bash

# Script to continuously monitor toxiproxy metrics with live CLI dashboard

METRICS_URL="localhost:8474/metrics"
HISTORY_FILE="/tmp/toxiproxy_metrics_history.txt"
MAX_HISTORY=20
REFRESH_INTERVAL=1

# Function to create a line graph
create_line_graph() {
    local history_file=$1
    local height=10
    local width=60

    # Read all values into an array
    local values=()
    local times=()
    while read -r time avg sum count; do
        if [ -n "$avg" ] && [ "$avg" != "0" ]; then
            values+=("$avg")
            times+=("$time")
        fi
    done < "$history_file"

    # If we don't have enough data points, return early
    if [ ${#values[@]} -lt 2 ]; then
        echo "Collecting data points... (need at least 2 for line graph)"
        return
    fi

    # Find min and max values for scaling
    local min_val=$(printf '%s\n' "${values[@]}" | sort -n | head -1)
    local max_val=$(printf '%s\n' "${values[@]}" | sort -n | tail -1)

    # If min and max are the same, adjust slightly for visualization
    if [ "$(echo "$min_val == $max_val" | bc -l)" = "1" ]; then
        max_val=$(echo "$max_val + 0.001" | bc -l)
    fi

    local range=$(echo "$max_val - $min_val" | bc -l)

    # Create the graph matrix
    declare -A graph

    # Plot points and connect with lines
    for ((i=0; i<${#values[@]}; i++)); do
        local x=$i
        local y_normalized=$(echo "scale=2; (${values[i]} - $min_val) / $range" | bc -l)
        local y=$(echo "scale=0; ($height - 1) - ($y_normalized * ($height - 1))" | bc -l)

        # Ensure y is within bounds
        if [ "$(echo "$y < 0" | bc -l)" = "1" ]; then y=0; fi
        if [ "$(echo "$y >= $height" | bc -l)" = "1" ]; then y=$((height-1)); fi

        # Convert y to integer for array indexing
        local y_int=$(printf "%.0f" "$y")
        graph[$x,$y_int]="●"

        # Draw line to next point
        if [ $((i+1)) -lt ${#values[@]} ]; then
            local next_y_normalized=$(echo "scale=2; (${values[i+1]} - $min_val) / $range" | bc -l)
            local next_y=$(echo "scale=0; ($height - 1) - ($next_y_normalized * ($height - 1))" | bc -l)

            if [ "$(echo "$next_y < 0" | bc -l)" = "1" ]; then next_y=0; fi
            if [ "$(echo "$next_y >= $height" | bc -l)" = "1" ]; then next_y=$((height-1)); fi

            # Simple line drawing between points
            local start_y_int=$(printf "%.0f" "$y")
            local end_y_int=$(printf "%.0f" "$next_y")
            if [ "$start_y_int" -gt "$end_y_int" ]; then
                for ((ly=end_y_int; ly<=start_y_int; ly++)); do
                    if [ -z "${graph[$x,$ly]}" ]; then
                        graph[$x,$ly]="│"
                    fi
                done
            else
                for ((ly=start_y_int; ly<=end_y_int; ly++)); do
                    if [ -z "${graph[$x,$ly]}" ]; then
                        graph[$x,$ly]="│"
                    fi
                done
            fi
        fi
    done

    # Display the graph
    echo "Connection Duration Line Graph (${#values[@]} points):"
    echo "┌────────────────────────────────────────────────────────────────┐"

    # Print from top to bottom
    for ((y=0; y<height; y++)); do
        printf "│"
        local y_val=$(echo "scale=4; $max_val - ($y * $range / ($height - 1))" | bc -l)
        printf "%7.4f │" "$y_val"

        for ((x=0; x<${#values[@]}; x++)); do
            if [ -n "${graph[$x,$y]}" ]; then
                printf "${graph[$x,$y]}"
            else
                printf " "
            fi
        done
        printf "\n"
    done

    echo "└────────────────────────────────────────────────────────────────┘"

    # Print time axis
    printf "         │"
    for ((i=0; i<${#times[@]}; i++)); do
        if (( i % 5 == 0 )) || (( i == ${#times[@]} - 1 )); then
            printf "${times[i]:6:2}"
        else
            printf " "
        fi
    done
    echo
}

# Function to clear screen and move cursor to top
clear_screen() {
    clear
}

# Function to get metrics and calculate average
get_metrics() {
    # Curl the metrics endpoint and filter for the specific toxiproxy metrics
    METRICS_OUTPUT=$(curl -s "$METRICS_URL" | grep -E "toxiproxy_proxy_connection_duration_seconds_(sum|count).*proxy=\"brain\"" | grep "listener=\"127.0.0.1:8081\"" | grep "upstream=\"vm.brainbit.io:80\"")

    # Check if curl was successful
    if [ ${PIPESTATUS[0]} -ne 0 ]; then
        echo "Error: Failed to fetch metrics from $METRICS_URL"
        return 1
    fi

    # Extract the sum and count values
    SUM=$(echo "$METRICS_OUTPUT" | grep "_sum" | awk '{print $2}')
    COUNT=$(echo "$METRICS_OUTPUT" | grep "_count" | awk '{print $2}')

    # Check if we got both values
    if [ -z "$SUM" ] || [ -z "$COUNT" ]; then
        echo "Error: Could not extract sum and count values from metrics"
        return 1
    fi

    # Calculate average (avoid division by zero)
    if [ "$COUNT" != "0" ]; then
        AVERAGE=$(echo "scale=6; $SUM / $COUNT" | bc -l)
    else
        AVERAGE="0"
    fi

    return 0
}

# Function to display dashboard
display_dashboard() {
    local timestamp=$1
    local sum=$2
    local count=$3
    local average=$4

    echo "╔══════════════════════════════════════════════════════════════════════════════╗"
    echo "║                       LIVE TOXIPROXY METRICS DASHBOARD                      ║"
    echo "╠══════════════════════════════════════════════════════════════════════════════╣"
    printf "║ Time: %-15s │ Sum: %-12s │ Count: %-8s │ Avg: %-8s ║\n" "$timestamp" "${sum}s" "$count" "${average}s"
    echo "╠══════════════════════════════════════════════════════════════════════════════╣"
    echo "║                        LIVE CONNECTION DURATION GRAPH                       ║"
    echo "╚══════════════════════════════════════════════════════════════════════════════╝"

    # Display line graph
    if [ -f "$HISTORY_FILE" ]; then
        echo
        create_line_graph "$HISTORY_FILE"
    else
        echo "No data available yet..."
    fi

    echo
    echo "📊 Current Metrics:"
    echo "   • Total connection time: ${sum} seconds"
    echo "   • Number of connections: ${count}"
    echo "   • Average per connection: ${average} seconds"

    # Add some visual indicators
    if (( $(echo "$average > 0.1" | bc -l) )); then
        echo "   ⚠️  High latency detected (>0.1s)"
    elif (( $(echo "$average > 0.05" | bc -l) )); then
        echo "   🟡 Moderate latency (>0.05s)"
    else
        echo "   ✅ Low latency (<0.05s)"
    fi

    echo
    echo "🔄 Refreshing every ${REFRESH_INTERVAL} second(s)... Press Ctrl+C to stop"
    echo "📁 History file: $HISTORY_FILE"
}

# Trap Ctrl+C to clean up
trap 'echo -e "\n\n👋 Stopping metrics monitoring..."; exit 0' INT

echo "🚀 Starting live toxiproxy metrics monitoring..."
echo "📡 Endpoint: $METRICS_URL"
echo "⏱️  Update interval: ${REFRESH_INTERVAL} second(s)"
echo ""
echo "Press Ctrl+C to stop"
sleep 2

# Main monitoring loop
while true; do
    # Get current timestamp
    TIMESTAMP=$(date '+%H:%M:%S')

    # Get metrics
    if get_metrics; then
        # Store current measurement in history file
        echo "$TIMESTAMP $AVERAGE $SUM $COUNT" >> "$HISTORY_FILE"

        # Keep only the last MAX_HISTORY entries
        tail -n $MAX_HISTORY "$HISTORY_FILE" > "${HISTORY_FILE}.tmp" && mv "${HISTORY_FILE}.tmp" "$HISTORY_FILE"

        # Clear screen and display dashboard
        clear_screen
        display_dashboard "$TIMESTAMP" "$SUM" "$COUNT" "$AVERAGE"
    else
        clear_screen
        echo "❌ Error fetching metrics at $(date '+%H:%M:%S')"
        echo "🔄 Retrying in ${REFRESH_INTERVAL} second(s)..."
    fi

    # Wait for next update
    sleep $REFRESH_INTERVAL
done
