package ebpf

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var defaultBuckets = []float64{0.01, 0.1, 1}

var _ = Describe("SlotsToConstHistogram", func() {
	It("should return zeros for empty slots", func() {
		var slots [MaxSlots]uint64
		count, sum, buckets := SlotsToConstHistogram(slots, defaultBuckets)

		Expect(count).To(Equal(uint64(0)))
		Expect(sum).To(Equal(float64(0)))
		Expect(buckets).To(HaveLen(len(defaultBuckets)))
		for _, v := range buckets {
			Expect(v).To(Equal(uint64(0)))
		}
	})

	It("should count fast operations into all buckets", func() {
		var slots [MaxSlots]uint64
		slots[5] = 100

		count, sum, buckets := SlotsToConstHistogram(slots, defaultBuckets)

		Expect(count).To(Equal(uint64(100)))
		Expect(sum).To(BeNumerically(">", 0))
		for _, b := range defaultBuckets {
			Expect(buckets[b]).To(Equal(uint64(100)))
		}
	})

	It("should not count slow operations in small buckets", func() {
		var slots [MaxSlots]uint64
		slots[21] = 50

		count, _, buckets := SlotsToConstHistogram(slots, defaultBuckets)

		Expect(count).To(Equal(uint64(50)))
		for _, b := range defaultBuckets {
			Expect(buckets[b]).To(Equal(uint64(0)))
		}
	})

	It("should produce cumulative bucket counts", func() {
		var slots [MaxSlots]uint64
		slots[5] = 10
		slots[15] = 20
		slots[19] = 30

		count, _, buckets := SlotsToConstHistogram(slots, defaultBuckets)

		Expect(count).To(Equal(uint64(60)))
		Expect(buckets).To(HaveKeyWithValue(0.01, uint64(10)))
		Expect(buckets).To(HaveKeyWithValue(0.1, uint64(30)))
		Expect(buckets).To(HaveKeyWithValue(1.0, uint64(60)))
	})

	It("should handle slots beyond the max bucket boundary", func() {
		var slots [MaxSlots]uint64
		slots[25] = 5
		slots[10] = 10

		count, _, buckets := SlotsToConstHistogram(slots, defaultBuckets)

		Expect(count).To(Equal(uint64(15)))
		Expect(buckets[0.01]).To(Equal(uint64(10)))
		Expect(buckets[1]).To(Equal(uint64(10)))
	})

	It("should work with custom bucket boundaries", func() {
		var slots [MaxSlots]uint64
		slots[5] = 10
		slots[19] = 20
		slots[23] = 30

		custom := []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}
		count, _, buckets := SlotsToConstHistogram(slots, custom)

		Expect(count).To(Equal(uint64(60)))
		Expect(buckets).To(HaveKeyWithValue(0.1, uint64(10)))
		Expect(buckets).To(HaveKeyWithValue(0.25, uint64(10)))
		Expect(buckets).To(HaveKeyWithValue(0.5, uint64(10)))
		Expect(buckets).To(HaveKeyWithValue(1.0, uint64(30)))
		Expect(buckets).To(HaveKeyWithValue(2.5, uint64(30)))
		Expect(buckets).To(HaveKeyWithValue(5.0, uint64(30)))
		Expect(buckets).To(HaveKeyWithValue(10.0, uint64(60)))
		Expect(buckets).To(HaveKeyWithValue(30.0, uint64(60)))
		Expect(buckets).To(HaveKeyWithValue(60.0, uint64(60)))
	})
})

var _ = DescribeTable("namespaceAllowed",
	func(namespaces []string, ns string, expected bool) {
		nsFilter := make(map[string]bool, len(namespaces))
		for _, n := range namespaces {
			nsFilter[n] = true
		}
		c := &Collector{namespaces: nsFilter}
		Expect(c.namespaceAllowed(ns)).To(Equal(expected))
	},
	Entry("empty filter allows all", nil, "anything", true),
	Entry("empty filter allows empty ns", nil, "", true),
	Entry("matching namespace allowed", []string{"default", "production"}, "default", true),
	Entry("non-matching namespace denied", []string{"default", "production"}, "staging", false),
	Entry("empty ns denied when filter set", []string{"default"}, "", false),
)
