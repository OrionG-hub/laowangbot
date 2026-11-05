package yvlu

import (
	"context"
	"image"
	"sync"
)

const emojiImageCacheLimit = 32
const emojiDownloadWorkers = 4

// Telegram document IDs identify immutable media. Only small decoded images
// are retained; failures and oversized images are retried on later requests.
type emojiImageCache struct {
	mu     sync.Mutex
	images map[int64]image.Image
	order  []int64
}

func (c *emojiImageCache) get(id int64) image.Image {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.images[id]
}

func (c *emojiImageCache) put(id int64, img image.Image) {
	if img == nil {
		return
	}
	bounds := img.Bounds()
	if bounds.Dx() < 1 || bounds.Dy() < 1 || bounds.Dx() > 256 || bounds.Dy() > 256 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.images == nil {
		c.images = make(map[int64]image.Image)
	}
	if _, ok := c.images[id]; ok {
		return
	}
	if len(c.order) >= emojiImageCacheLimit {
		delete(c.images, c.order[0])
		c.order = c.order[1:]
	}
	c.images[id] = img
	c.order = append(c.order, id)
}

// Independent resources can be fetched together without changing message
// order. The worker count bounds requests and decoded images in flight.
func parallelImages(ctx context.Context, keys []string, load func(context.Context, string) image.Image) map[string]image.Image {
	images := make(map[string]image.Image)
	workers := min(emojiDownloadWorkers, len(keys))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			for i := start; i < len(keys); i += workers {
				if ctx.Err() != nil {
					return
				}
				if img := load(ctx, keys[i]); img != nil {
					mu.Lock()
					images[keys[i]] = img
					mu.Unlock()
				}
			}
		}(worker)
	}
	wg.Wait()
	return images
}
