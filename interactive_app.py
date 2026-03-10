def process_request(name):
    print(f"Processing for {name}...")
    save_to_db(name)

def save_to_db(data):
    print(f"Data {data} saved.")

if __name__ == "__main__":
    print("Interactive App Started. Press Enter to simulate a request, or type 'quit' to exit.")
    while True:
        cmd = input("> ")
        if cmd == "quit":
            break
        process_request("User_Action")
