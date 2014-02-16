package main
import (
    "log"
    "blkparser"
    "sync"
    "sync/atomic"
    "time"
    "fmt"
    "github.com/garyburd/redigo/redis"
    "os"
    "os/signal"
    "encoding/json"
    "github.com/pmylund/go-cache"
    "github.com/jmhodges/levigo"
    "runtime"
)

type Block struct {
    Hash        string  `json:"hash"`
    Height      uint    `json:"height"`
//    Txs         []*Tx   `json:"tx,omitempty" bson:"-"`
    Version     uint32  `json:"ver"`
    MerkleRoot  string  `json:"mrkl_root"`
    BlockTime   uint32  `json:"time"`
    Bits        uint32  `json:"bits"`
    Nonce       uint32  `json:"nonce"`
    Size        uint32  `json:"size"`
    TxCnt       uint32  `json:"n_tx"`
    TotalBTC    uint64  `json:"total_out"`
//    BlockReward float64 `json:"-"`
    Parent      string  `json:"prev_block"`
//    Next        string  `json:"next_block"`
}

type Tx struct {
    Hash          string         `json:"hash"`
    Index uint32 `json:"-"`
    Size          uint32         `json:"size"`
    LockTime      uint32         `json:"lock_time"`
    Version       uint32         `json:"ver"`
    TxInCnt       uint32         `json:"vin_sz"`
    TxOutCnt      uint32         `json:"vout_sz"`
    TxIns         []*TxIn        `json:"in" bson:"-"`
    TxOuts        []*TxOut       `json:"out" bson:"-"`
    TotalOut      uint64         `json:"vout_total"`
    TotalIn       uint64         `json:"vin_total"`
    BlockHash     string         `json:"block_hash"`
    BlockHeight   uint           `json:"block_height"`
    BlockTime     uint32         `json:"block_time"`
}

type TxOut struct {
    TxHash     string    `json:"-"`
    BlockHash     string    `json:"-"`
    BlockTime     uint32    `json:"-"`
    Addr     string    `json:"hash"`
    Value    uint64    `json:"value"`
    Index    uint32    `json:"n"`
    Spent    *TxoSpent `json:"spent,omitempty"`
}

type TxOutCached struct {
    Id string `json:"id"`
    Addr     string    `json:"hash"`
    Value    uint64    `json:"value"`
}

type PrevOut struct {
    Hash    string `json:"hash"`
    Vout    uint32 `json:"n"`
    Address string `json:"address"`
    Value   uint64 `json:"value"`
}


type TxIn struct {
    TxHash     string    `json:"-"`
    BlockHash     string    `json:"-"`
    BlockTime     uint32    `json:"-"`
    PrevOut   *PrevOut `json:"prev_out"`   
    Index    uint32    `json:"n"`
}

type TxoSpent struct {
    Spent       bool   `json:"spent"`
    BlockHeight uint32 `json:"block_height"`
    InputHash   string `json:"tx_hash"`
    InputIndex  uint32 `json:"in_index"`
}

var wg, txowg, txiwg sync.WaitGroup
var running bool

func getGOMAXPROCS() int {
    return runtime.GOMAXPROCS(0)
}

func main () {
    fmt.Printf("GOMAXPROCS is %d\n", getGOMAXPROCS())
    opts := levigo.NewOptions()
    opts.SetCreateIfMissing(true)
    filter := levigo.NewBloomFilter(10)
    opts.SetFilterPolicy(filter)
    ldb, err := levigo.Open("/home/thomas/btcplex_txocached100c", opts) //alpha

    defer ldb.Close()

    if err != nil {
        fmt.Printf("failed to load db: %s\n", err)
    }
    
    wo := levigo.NewWriteOptions()
    //wo.SetSync(true)
    defer wo.Close()

    ro := levigo.NewReadOptions()
    defer ro.Close()

    wb := levigo.NewWriteBatch()
    defer wb.Close()

    // Redis connect
    // Used for pub/sub in the webapp and data like latest processed height
    server := "localhost:6381"
    pool := &redis.Pool{
             MaxIdle: 3,
             IdleTimeout: 240 * time.Second,
             Dial: func () (redis.Conn, error) {
                 c, err := redis.Dial("tcp", server)
                 if err != nil {
                     return nil, err
                 }
//                 if _, err := c.Do("AUTH", password); err != nil {
//                     c.Close()
//                     return nil, err
//               }
//                 return c, err
                return c, err
             },
                TestOnBorrow: func(c redis.Conn, t time.Time) error {
                    _, err := c.Do("PING")
                 return err
                },
         }
    conn := pool.Get()
    defer conn.Close()

    //latestheight, _ := redis.Int(conn.Do("GET", "height:latest"))
    latestheight := 0
    log.Printf("Latest height: %v\n", latestheight)

    running = true
    cs := make(chan os.Signal, 1)
    signal.Notify(cs, os.Interrupt)
    go func() {
        for sig := range cs {
            running = false
            log.Printf("Captured %v, waiting for everything to finish...\n", sig)
            wg.Wait()
            defer os.Exit(1)
        }
    }()

    c := cache.New(5*time.Minute, 30*time.Second)

    log.Println("DB Loaded")


    concurrency := 50
    sem := make(chan bool, concurrency)
    //concurrency := 50
    //sem := make(chan bool, concurrency)
    //for i := 0; i < cap(sem); i++ {
    //    sem <- true
    //}

    // Real network magic byte
    blockchain, _ := blkparser.NewBlockchain("/box/bitcoind_data/blocks", [4]byte{0xF9,0xBE,0xB4,0xD9})

    //txscounter := ratecounter.NewRateCounter(60 * time.Second)
    //blockscounter := ratecounter.NewRateCounter(60 * time.Second)
    
    //ticker := time.NewTicker(10 * time.Second)
    //go func() {
    //    for _ = range ticker.C {
    //        log.Printf("Blocks/min: %v | Tx/min: %v\n", blockscounter.Rate(), txscounter.Rate())
    //    }
    //}()
    //autos := true
    block_height := uint(0)
    //if latestheight != 0 {
    //POS:26031147, 582014/02/08 14:53:36 Current block: 234537 
    //POS:95487936, 722014/02/09 19:51:54 Current block: 249390
    //POS:, 722014/02/09 19:51:16 Current block: 
    //    err = blockchain.SkipTo(uint32(72), int64(94891519))
    //    block_height = 249383
    //    autos = false
    //    if err != nil {
    //        log.Println("Error blkparser: blockchain.SkipTo")
    //        os.Exit(1)
    //    }
    //}
    for i := uint(0); i < 280000; i++ {
        if !running {
            break
        }

        wg.Add(1)

        bl, er := blockchain.NextBlock()
        if er!=nil {
            wg.Wait()
            log.Println("END of DB file")
            break
        }

        bl.Raw = nil

        if bl.Parent == "" {
            block_height = uint(0)
            conn.Do("HSET", fmt.Sprintf("block:%v:h", bl.Hash), "main", true)

        } else {
            prev_height, found := c.Get(bl.Parent)
            if found {
                block_height = uint(prev_height.(uint) + 1)
            }

            //if autos {
                prevheight := block_height - 1
                prevhashtest := bl.Parent
                prevnext := bl.Hash
                for {
                    prevkey := fmt.Sprintf("height:%v", prevheight)
                    prevcnt, _ := redis.Int(conn.Do("ZCARD", prevkey))
                    // SSDB doesn't support negative slice yet
                    prevs, _ := redis.Strings(conn.Do("ZRANGE", prevkey, 0, prevcnt - 1))
                    for _, cprevhash := range prevs {
                        if cprevhash == prevhashtest {
                            // current block parent
                            prevhashtest, _ = redis.String(conn.Do("HGET", fmt.Sprintf("block:%v:h", cprevhash), "parent"))
                            // Set main to 1 and the next => prevnext
                            conn.Do("HMSET", fmt.Sprintf("block:%v:h", cprevhash), "main", true, "next", prevnext)
                            conn.Do("SET", fmt.Sprintf("block:height:%v", prevheight), cprevhash)
                            prevnext = cprevhash
                        } else {
                            // Set main to 0
                            conn.Do("HSET", fmt.Sprintf("block:%v:h", cprevhash), "main", false)
                        }
                    }
                    if len(prevs) == 1 {
                        break
                    }
                    prevheight--    
                }    
            //}
            
           
        }
        c.Set(bl.Hash, block_height, 30*time.Minute)

        // Orphans blocks handling
        conn.Do("ZADD", fmt.Sprintf("height:%v", block_height), bl.BlockTime, bl.Hash)
        conn.Do("HSET", fmt.Sprintf("block:%v:h", bl.Hash), "parent", bl.Parent)
 
        if latestheight != 0 && !(latestheight + 1 <= int(block_height)) {
            log.Printf("Skipping block #%v\n", block_height)
            continue
        }
        
        log.Printf("Current block: %v (%v)\n", block_height, bl.Hash)
        
        block := new(Block)
        block.Hash = bl.Hash
        block.Height = block_height
        block.Version = bl.Version
        block.MerkleRoot = bl.MerkleRoot
        block.BlockTime = bl.BlockTime
        block.Bits = bl.Bits
        block.Nonce = bl.Nonce
        block.Size = bl.Size
        block.Parent = bl.Parent

        txs := []*Tx{}

        total_bl_out := uint64(0)
        for tx_index, tx := range bl.Txs {
            //log.Printf("Tx #%v: %v\n", tx_index, tx.Hash)
            
            total_tx_out := uint64(0)
            total_tx_in := uint64(0)

            //conn.Send("MULTI")
            //txos := []*TxOut{}
            txos_cnt := uint32(0)
            for txo_index, txo := range tx.TxOuts {
                txowg.Add(1)
                sem <-true
                go func(bl *blkparser.Block, tx *blkparser.Tx, pool *redis.Pool, total_tx_out *uint64, txos_cnt *uint32, txo *blkparser.TxOut, txo_index int) {
                    conn := pool.Get()
                    defer conn.Close()
                    defer func() {
                        <-sem
                    }()
                    defer txowg.Done()
                    atomic.AddUint64(total_tx_out, uint64(txo.Value))
                    //txos = append(txos, ntxo)
                    atomic.AddUint32(txos_cnt, 1)

                    ntxo := new(TxOut)
                    ntxo.TxHash = tx.Hash
                    ntxo.BlockHash = bl.Hash
                    ntxo.BlockTime = bl.BlockTime
                    ntxo.Addr = txo.Addr
                    ntxo.Value = txo.Value
                    ntxo.Index = uint32(txo_index)
                    txospent := new(TxoSpent)
                    ntxo.Spent = txospent
                    ntxocached := new(TxOutCached)
                    ntxocached.Addr = txo.Addr
                    ntxocached.Value = txo.Value

                    ntxocachedjson, _ := json.Marshal(ntxocached)
                    ldb.Put(wo, []byte(fmt.Sprintf("txo:%v:%v", tx.Hash, txo_index)), ntxocachedjson)

                    ntxojson, _ := json.Marshal(ntxo)
                    ntxokey := fmt.Sprintf("txo:%v:%v", tx.Hash, txo_index)
                    conn.Do("SET", ntxokey, ntxojson)

                    
                    //conn.Send("ZADD", fmt.Sprintf("txo:%v", tx.Hash), txo_index, ntxokey)
                    conn.Do("ZADD", fmt.Sprintf("addr:%v", ntxo.Addr), bl.BlockTime, tx.Hash)
                    conn.Do("ZADD", fmt.Sprintf("addr:%v:received", ntxo.Addr), bl.BlockTime, tx.Hash)

                    conn.Do("HINCRBY", fmt.Sprintf("addr:%v:h", ntxo.Addr), "tr", ntxo.Value)
                }(bl, tx, pool, &total_tx_out, &txos_cnt, txo, txo_index)
            }
            txowg.Wait()

            err := ldb.Write(wo, wb)
            if err != nil {
                log.Fatalf("Err write batch: %v", err)
            }
            wb.Clear()
            
            //txis := []*TxIn{}
            txis_cnt := uint32(0)
            // Skip the ins if it's a CoinBase Tx (1 TxIn for newly generated coins)
            if !(len(tx.TxIns) == 1 && tx.TxIns[0].InputVout==0xffffffff)  {
                
                //conn.Send("MULTI")

                for txi_index, txi := range tx.TxIns {
                    txiwg.Add(1)
                    sem <-true
                    go func(txi *blkparser.TxIn, bl *blkparser.Block, tx *blkparser.Tx, pool *redis.Pool, txis_cnt *uint32, total_tx_in *uint64, txi_index int) {
                        conn := pool.Get()
                        defer conn.Close()
                        defer func() {
                            <-sem
                        }()
                        defer txiwg.Done()
                    
                        ntxi := new(TxIn)
                        ntxi.TxHash = tx.Hash
                        ntxi.BlockHash = bl.Hash
                        ntxi.BlockTime = bl.BlockTime
                        ntxi.Index = uint32(txi_index)
                        nprevout := new(PrevOut)
                        nprevout.Vout = txi.InputVout
                        nprevout.Hash = txi.InputHash
                        ntxi.PrevOut = nprevout
                        prevtxo := new(TxOutCached)     
                        
                        prevtxocachedraw, err := ldb.Get(ro, []byte(fmt.Sprintf("txo:%v:%v", txi.InputHash, txi.InputVout)))
                        if err != nil {
                            log.Printf("Err getting prevtxocached: %v", err)
                        }

                        if len(prevtxocachedraw) > 0 {
                            if err := json.Unmarshal(prevtxocachedraw, prevtxo); err != nil {
                                panic(err)
                            }
                        } else {
                            //log.Println("Fallback to SSDB")
                            prevtxoredisjson, err := redis.String(conn.Do("GET", fmt.Sprintf("txo:%v:%v", txi.InputHash, txi.InputVout)))
                            if err != nil {
                                log.Printf("KEY:%v\n", fmt.Sprintf("txo:%v:%v", txi.InputHash, txi.InputVout))
                                panic(err)
                            }
                            prevtxoredis := new(TxOut)
                            json.Unmarshal([]byte(prevtxoredisjson), prevtxoredis)

                            // If something  goes wrong with LevelDB, no problem, we query MongoDB
                            //log.Println("Fallback to MongoDB")
                            //prevtxomongo := new(TxOut)
                            //if err := db.C("txos").Find(bson.M{"txhash":txi.InputHash, "index": txi.InputVout}).One(prevtxomongo); err != nil {
                            //    log.Printf("TXO requested as prevtxo: %v\n", txi.InputHash)
                            //    panic(err)                            
                            //}
                            prevtxo.Addr = prevtxoredis.Addr
                            prevtxo.Value = prevtxoredis.Value
                            //prevtxo.Id = prevtxomongo.Id.Hex()
                        }

                        //go func(txi *blkparser.TxIn) {
                        ldb.Delete(wo, []byte(fmt.Sprintf("txo:%v:%v", txi.InputHash, txi.InputVout)))
                        //}(txi)
                        
                        //} else {
                        //    for i := 1; i < 11; i++ {
                        //        err = db.C("txos").Find(bson.M{"txhash":txi.InputHash, "index": txi.InputVout}).One(prevtxo)
                        //        if err != nil {
                        //            if i == 10 {
                        //                panic(fmt.Sprintf("Can't find previous TXO for TXI: %+v, err:%v", txi, err))
                        //            }
                        //            log.Printf("Can't find previous TXO for TXI: %+v, err:%v\n", txi, err)
                        //            time.Sleep(time.Duration(i*5000)*time.Millisecond)
                        //            continue
                        //        }   
                        //    }
                        //}

                        nprevout.Address = prevtxo.Addr
                        nprevout.Value = prevtxo.Value
                        

                        txospent := new(TxoSpent)
                        txospent.Spent = true
                        txospent.BlockHeight = uint32(block_height)
                        txospent.InputHash = tx.Hash
                        txospent.InputIndex = uint32(txi_index)

                        //total_tx_in+= uint(nprevout.Value)
                        atomic.AddUint64(total_tx_in, nprevout.Value)
                        
                        //txis = append(txis, ntxi)
                        atomic.AddUint32(txis_cnt, 1)

                        //log.Println("Starting update prev txo")
                        ntxijson, _ := json.Marshal(ntxi)
                        ntxikey := fmt.Sprintf("txi:%v:%v", tx.Hash, txi_index)

                        txospentjson, _ := json.Marshal(txospent)

                        conn.Do("SET", ntxikey, ntxijson)
                        //conn.Send("ZADD", fmt.Sprintf("txi:%v", tx.Hash), txi_index, ntxikey)

                        conn.Do("SET", fmt.Sprintf("txo:%v:%v:spent", txi.InputHash, txi.InputVout), txospentjson)

                        conn.Do("ZADD", fmt.Sprintf("addr:%v", nprevout.Address), bl.BlockTime, tx.Hash)
                        conn.Do("ZADD", fmt.Sprintf("addr:%v:sent", nprevout.Address), bl.BlockTime, tx.Hash)
                        conn.Do("HINCRBY", fmt.Sprintf("addr:%v:h", nprevout.Address), "ts", nprevout.Value)
                    }(txi, bl, tx, pool, &txis_cnt, &total_tx_in, txi_index)
                    
                }
                //r, err := conn.Do("EXEC")
                //if err != nil {
                //    panic(err)
                //}
            }

            txiwg.Wait()

            total_bl_out+= total_tx_out

            ntx := new(Tx)
            ntx.Index = uint32(tx_index)
            ntx.Hash = tx.Hash
            ntx.Size = tx.Size
            ntx.LockTime = tx.LockTime
            ntx.Version = tx.Version
            ntx.TxInCnt = uint32(txis_cnt)
            ntx.TxOutCnt = uint32(txos_cnt)
            ntx.TotalOut = uint64(total_tx_out)
            ntx.TotalIn = uint64(total_tx_in)
            ntx.BlockHash = bl.Hash
            ntx.BlockHeight = block_height
            ntx.BlockTime = bl.BlockTime

            ntxjson, _ := json.Marshal(ntx)
            //conn.Send("MULTI")
            ntxjsonkey := fmt.Sprintf("tx:%v", ntx.Hash)
            conn.Do("SET", ntxjsonkey, ntxjson)
            conn.Do("ZADD", fmt.Sprintf("block:%v:txs", block.Hash), tx_index, ntxjsonkey)
            //conn.Do("EXEC")
            txs = append(txs, ntx)
            //txscounter.Mark()
        }

        block.TotalBTC = uint64(total_bl_out)
        block.TxCnt = uint32(len(txs))

        blockjson, _ := json.Marshal(block)
        conn.Do("ZADD", "blocks", block.BlockTime, block.Hash)
        conn.Do("MSET", fmt.Sprintf("block:%v", block.Hash), blockjson, "height:latest", int(block_height), fmt.Sprintf("block:height:%v", block.Height), block.Hash)
        //blockscounter.Mark()
        
        if !running {
            log.Printf("Done. Stopped at height: %v.", block_height)
        }

        wg.Done()
    }
    wg.Wait()
}