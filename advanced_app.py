import time
import os

def nested_call_1():
    print("  -> Level 1")
    time.sleep(0.1)
    nested_call_2()

def nested_call_2():
    print("    -> Level 2")
    time.sleep(0.1)
    nested_call_3()

def nested_call_3():
    print("      -> Level 3 (Deep!)")
    time.sleep(0.1)

def run():
    print("Advanced App Running...")
    while True:
        val = input("\nType 'run' to trigger nested flow, or 'quit': ")
        if val == 'quit':
            break
        if val == 'run':
            nested_call_1()

if __name__ == "__main__":
    run()
