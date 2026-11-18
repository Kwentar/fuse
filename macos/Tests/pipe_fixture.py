import json,sys
sys.stderr.write('x' * (256 * 1024))
sys.stderr.flush()
payload=sys.stdin.read()
print(json.dumps({'text':payload,'arguments':sys.argv[1:]}))
print(json.dumps({'message_id':'550e8400-e29b-41d4-a716-446655440000','status':'delivery_unknown','code':'ACK_TIMEOUT'}))
sys.stderr.write('\n'+json.dumps({'error':'IPC_RESULT_UNKNOWN'})+'\n')
sys.exit(4)
