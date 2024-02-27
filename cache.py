import asyncio
from typing import List, Tuple
import bittensor as bt

async def sync_miners(n: int):
    indices = bt.torch.topk(metagraph.incentive, n).indices

    # Get the corresponding uids
    uids_with_highest_incentives: List[int] = metagraph.uids[indices].tolist()

    # get the axon of the uids
    axons: List[Tuple[bt.axon, int]] = [
        (metagraph.axons[uid], uid) for uid in uids_with_highest_incentives
    ]
    for axon, uid in axons:
        print(axon.ip, axon.port, metagraph.incentive[uid].item())
    await asyncio.sleep(50 * 12)


if __name__ == "__main__":
    subtensor = bt.subtensor("ws://subtensor.sybil.com:9944")
    metagraph: bt.metagraph = subtensor.metagraph(netuid=4)

    wallet = bt.wallet(name="targon")

    dendrite = bt.dendrite(wallet=wallet)
    while True:
        asyncio.run(sync_miners(10))
