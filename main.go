/**
 * @version: 1.0.0
 * @author: zhangguodong:general_zgd
 * @license: LGPL v3
 * @contact: general_zgd@163.com
 * @site: github.com/generalzgd
 * @software: GoLand
 * @file: main.go
 * @time: 2018/12/19 15:45
 */
package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"svr-frame/libs"

	"github.com/PuerkitoBio/goquery"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
)

func init() {
	libs.DetectDir(libs.LogsDir)

	logger := logs.GetBeeLogger()
	logger.SetLevel(beego.LevelInformational)
	logger.SetLogger(logs.AdapterConsole)
	logger.SetLogger(logs.AdapterFile, `{"filename":"logs/file.log","level":7,"maxlines":1024000000,"maxsize":1024000000,"daily":true,"maxdays":7}`)
	logger.EnableFuncCallDepth(true)
	logger.SetLogFuncCallDepth(4)
	logger.Async(100000)
}

func exit() {
	logs.GetBeeLogger().Flush()
	os.Exit(1)
}

type CheckPinyin struct {
	Txt   string
	OriPy string
	DstPy string
}

func inputWork(flow chan<- *CheckPinyin, start uint32, filename string) {
	defer func() {
		close(flow)
	}()

	path := filepath.Join(filepath.Dir(os.Args[0]), "data", filename)

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	rd := bufio.NewReader(f)
	cnt := uint32(0)
	for {
		cnt += 1
		l, _, err := rd.ReadLine()
		if err == io.EOF {
			return
		}
		if cnt <= start {
			continue
		}
		line := strings.TrimSpace(string(l))
		if len(line) == 0 {
			continue
		}

		arr := strings.Split(line, "=")
		if len(arr) != 2 {
			continue
		}

		it := CheckPinyin{}
		it.Txt = strings.TrimSpace(arr[0])
		it.OriPy = strings.TrimSpace(arr[1])

		if len(it.Txt) == 0 {
			continue
		}

		flow <- &it
	}
}

func checkPhraseWork(inflow <-chan *CheckPinyin, outflow chan<- *CheckPinyin, missflow chan<- *CheckPinyin, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
	}()

	for it := range inflow {
		cnt := atomic.AddUint32(&checkCnt, 1)

		url := fmt.Sprintf("https://hanyu.baidu.com/s?wd=%s&from=zici", it.Txt)
		res, err := http.Get(url)
		if err != nil {
			beego.Error("get url fail.", err)
			missflow <- it
			continue
		}
		if res.StatusCode != 200 {
			beego.Info("res.StatusCode：", res.StatusCode)
			missflow <- it
			continue
		}
		doc, err := goquery.NewDocumentFromReader(res.Body)
		if err != nil {
			beego.Error("NewDocumentFromReader err.", err)
			res.Body.Close()
			missflow <- it
			continue
		}
		res.Body.Close()

		//pronounceList := []string{}
		//#是id .是class
		div := doc.Find(".header-info #pinyin h2 span b")
		txt := strings.TrimSpace(div.Text())

		txt = strings.TrimLeft(txt, "[")
		txt = strings.TrimRight(txt, "]")
		txt = strings.TrimSpace(txt)

		txt = strings.Replace(txt, " ", ",", -1)


		it.DstPy = txt
		beego.Info("info:", cnt, it.Txt, it.DstPy)

		if len(it.DstPy) > 0 && it.OriPy != it.DstPy {
			beego.Info("find pinyin diff:", it.OriPy, it.DstPy)
		}

		outflow <- it

		time.Sleep(time.Millisecond * 100)
	}
}

func checkWork(inflow <-chan *CheckPinyin, outflow chan<- *CheckPinyin, missedFlow chan<- *CheckPinyin, wg *sync.WaitGroup) {
	defer func() {
		//close(outflow)
		wg.Done()
	}()

	for it := range inflow {
		cnt := atomic.AddUint32(&checkCnt, 1)
		url := fmt.Sprintf("https://hanyu.baidu.com/s?wd=%s&from=zici", it.Txt)
		res, err := http.Get(url)
		if err != nil {
			beego.Error("get url fail.", err)
			missedFlow <- it
			continue
		}
		if res.StatusCode != 200 {
			beego.Info("res.StatusCode：", res.StatusCode)
			missedFlow <- it
			continue
		}
		doc, err := goquery.NewDocumentFromReader(res.Body)
		if err != nil {
			beego.Error("NewDocumentFromReader err.", err)
			res.Body.Close()
			missedFlow <- it
			continue
		}
		res.Body.Close()

		pronounceList := []string{}
		//#是id .是class
		doc.Find(".pronounce b").Each(func(i int, s *goquery.Selection) {
			pronounceList = append(pronounceList, strings.TrimSpace(s.Text()))
		})
		it.DstPy = strings.Join(pronounceList, ",")
		beego.Info("info:", cnt, it.Txt, it.DstPy)

		if len(it.DstPy) > 0 && it.OriPy != it.DstPy {
			beego.Info("find pinyin diff:", it.OriPy, it.DstPy)
		}

		outflow <- it

		time.Sleep(time.Millisecond * 100)
	}
}

func packWork(flow <-chan *CheckPinyin, filename string) {
	path := filepath.Join(filepath.Dir(os.Args[0]), "data", filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, os.ModePerm)
	if err != nil {
		return
	}
	defer f.Close()

	for it := range flow {
		if len(it.DstPy) == 0 {
			it.DstPy = it.OriPy
		}
		line := fmt.Sprintf("%s=%s\n", it.Txt, it.DstPy)
		n, err := f.WriteString(line)
		beego.Info(n, err)
	}
}

var (
	checkCnt = uint32(0)
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	//生产者
	makeFlow := make(chan *CheckPinyin, 10000)
	//检测者
	checkedFlow := make(chan *CheckPinyin, 10000)

	missedFlow := make(chan *CheckPinyin, 10000)

	go inputWork(makeFlow, checkCnt, "pinyin.db")

	wg := sync.WaitGroup{}
	for i := 0; i < 25; i += 1 {
		wg.Add(1)
		go checkWork(makeFlow, checkedFlow, missedFlow, &wg)
	}

	go func() {
		wg.Wait()
		close(checkedFlow)
		close(missedFlow)
	}()

	go packWork(missedFlow, "pinyin_missed.db")

	packWork(checkedFlow, "pinyin_checked.db")

	// catchs system signal
	//chSig := make(chan os.Signal)
	//signal.Notify(chSig, syscall.SIGINT, syscall.SIGTERM)
	//fmt.Println("Signal: ", <-chSig)
}
