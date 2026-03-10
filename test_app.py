def hello():
    print("Hello from python")
    nested()

def nested():
    print("I'm nested")

if __name__ == "__main__":
    hello()
    nested()
