import time

def tick():
    print("Tick...")
    tock()

def tock():
    print("Tock...")

if __name__ == "__main__":
    print("Loop app starting...")
    for i in range(5):
        time.sleep(1)
        tick()
